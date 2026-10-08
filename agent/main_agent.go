package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"github.com/hpidcock/lxd-images-freebsd/agent/internal/filesystem"
	"github.com/hpidcock/lxd-images-freebsd/agent/internal/sysutil"
)

var servers = make(map[string]*http.Server, 2)
var errChan = make(chan error)

type cmdAgent struct {
	global *cmdGlobal
}

// Command line for lxd-agent.
func (c *cmdAgent) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "lxd-agent [--debug]"
	cmd.Short = "LXD virtual machine agent"
	cmd.Long = `Description:
  LXD virtual machine agent

  This daemon is to be run inside virtual machines managed by LXD.
  It will normally be started through init scripts present or injected
  into the virtual machine.
`
	cmd.RunE = c.Run

	return cmd
}

// Run executes the agent command.
func (c *cmdAgent) Run(cmd *cobra.Command, args []string) error {
	// Setup logger.
	err := logger.InitLogger("", syslogName, c.global.flagLogVerbose, c.global.flagLogDebug, nil)
	if err != nil {
		// Ensure we exit with a non-zero exit code.
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1) //nolint:revive
	}

	logger.Info("Starting")
	defer logger.Info("Stopped")

	// Apply the templated files.
	_, err = templatesApply("files/")
	if err != nil {
		return err
	}

	reconfigureNetworkInterfaces()

	// Load the kernel drivers.
	for _, module := range vsockModules {
		err = sysutil.LoadModule(module)
		if err != nil {
			return fmt.Errorf("Cannot load the %s kernel module: %w", module, err)
		}
	}

	// Mount shares from host.
	c.mountHostShares()

	d := newDaemon(c.global.flagLogDebug, c.global.flagLogVerbose)

	err = d.init()
	if err != nil {
		return fmt.Errorf("Failed initialising daemon: %w", err)
	}

	// Create a cancellation context.
	ctx, cancelFunc := context.WithCancel(context.Background())

	// Start status notifier in background.
	cancelStatusNotifier := c.startStatusNotifier(ctx, d.chConnected)

	// Cancel context when SIGTEM is received.
	chSignal := make(chan os.Signal, 1)
	signal.Notify(chSignal, unix.SIGTERM)

	exitStatus := 0

	select {
	case <-chSignal:
	case err := <-errChan:
		fmt.Fprintln(os.Stderr, err)
		exitStatus = 1
	}

	cancelStatusNotifier() // Ensure STOPPED status is written to QEMU status ringbuffer.
	cancelFunc()

	// Ensure we exit with a relevant exit code.
	os.Exit(exitStatus) //nolint:revive

	return nil
}

// startStatusNotifier sends status of agent to vserial ring buffer every 5s or when context is done.
// Returns a function that can be used to update the running status to STOPPED in the ring buffer.
func (c *cmdAgent) startStatusNotifier(ctx context.Context, chConnected <-chan struct{}) context.CancelFunc {
	// Write initial started status.
	_ = c.writeStatus("STARTED")

	wg := sync.WaitGroup{}
	exitCtx, exit := context.WithCancel(ctx) // Allows manual synchronous cancellation via cancel function.
	cancel := func() {
		exit()    // Signal for the go routine to end.
		wg.Wait() // Wait for the go routine to actually finish.
	}

	wg.Go(func() {
		ticker := time.NewTicker(time.Second * 5)
		defer ticker.Stop()

		for {
			select {
			case <-chConnected:
				_ = c.writeStatus("CONNECTED") // Indicate we were able to connect to LXD.
			case <-ticker.C:
				_ = c.writeStatus("STARTED") // Re-populate status periodically in case LXD restarts.
			case <-exitCtx.Done():
				_ = c.writeStatus("STOPPED") // Indicate we are stopping to LXD and exit go routine.
				return
			}
		}
	})

	return cancel
}

// writeStatus writes a status code to the vserial ring buffer used to detect agent status on host.
func (c *cmdAgent) writeStatus(status string) error {
	vSerial, err := os.OpenFile(sysutil.AgentStatusDevice, os.O_RDWR|unix.O_NOCTTY, 0600)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}

		return err
	}

	defer vSerial.Close()

	err = sysutil.PrepareStatusDevice(vSerial)
	if err != nil {
		return err
	}

	_, err = vSerial.Write([]byte(status + "\n"))
	if err != nil {
		return err
	}

	return nil
}

// mountHostShares reads the agent-mounts.json file from config share and mounts the shares requested.
func (c *cmdAgent) mountHostShares() {
	agentMountsFile := "./agent-mounts.json"

	b, err := os.ReadFile(agentMountsFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return
		}

		logger.Errorf("Failed loading agent mounts file %q: %v", agentMountsFile, err)
		return
	}

	var agentMounts []instancetype.VMAgentMount
	err = json.Unmarshal(b, &agentMounts)
	if err != nil {
		logger.Errorf("Failed parsing agent mounts file %q: %v", agentMountsFile, err)
		return
	}

	for _, mount := range agentMounts {
		l := logger.AddContext(logger.Ctx{"source": mount.Source, "path": mount.Target})

		if strings.Contains(mount.Target, "..") {
			l.Error("Invalid mount target")
			continue
		}

		// Convert relative mounts to absolute from / otherwise dir creation fails or mount fails.
		if !strings.HasPrefix(mount.Target, "/") {
			mount.Target = "/" + mount.Target
			l.AddContext(logger.Ctx{"path": mount.Target})
		}

		if filesystem.IsMountPoint(mount.Target) {
			// Already mounted.
			continue
		}

		err := os.MkdirAll(mount.Target, 0755)
		if err != nil {
			l.Error("Failed creating mount target", logger.Ctx{"err": err})
			continue // Do not try to mount if mount point cannot be created.
		}

		args, err := sysutil.MountArgs(mount.FSType, mount.Source, mount.Target, mount.Options)
		if err != nil {
			l.Error("Unsupported mount", logger.Ctx{"err": err, "type": mount.FSType})
			continue
		}

		_, err = shared.RunCommand(context.TODO(), "mount", args...)
		if err != nil {
			l.Error("Failed mounting", logger.Ctx{"err": err, "args": args})
			continue
		}

		l.Info("Mounted", logger.Ctx{"type": mount.FSType})
	}
}
