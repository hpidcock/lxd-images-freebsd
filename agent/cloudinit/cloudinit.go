// Package cloudinit implements "lxd-agent cloud-init": it fetches the
// instance's cloud-init configuration from LXD through the devlxd socket and
// applies it with FreeBSD's nuageinit(7), the base system's limited cloud-init
// implementation.
//
// LXD does not put the cloud-init data on the config drive; Linux guests get it
// through cloud-init's LXD datasource, which talks to the devlxd socket the
// agent proxies to the host. This command plays that role for nuageinit: it
// writes a NoCloud style seed directory (meta-data, user-data, network-config)
// and runs "nuageinit <seed> nocloud". The post-network steps (packages,
// users, chpasswd, runcmd, user-data scripts) are run later in the boot by the
// lxd_cloudinit rc scripts from the files nuageinit leaves in /var/cache/nuageinit.
package cloudinit

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v2"

	"github.com/hpidcock/lxd-images-freebsd/agent/internal/devlxd"
)

type cmdCloudInit struct {
	socket    string
	stateDir  string
	seedDir   string
	runDir    string
	nuageinit string
	timeout   time.Duration
	force     bool
}

// Command returns the cloud-init subcommand.
func Command() *cobra.Command {
	c := &cmdCloudInit{}

	cmd := &cobra.Command{
		Use:   "cloud-init",
		Short: "Apply the instance's cloud-init configuration with nuageinit",
		Long: `Description:
  Fetch the cloud-init meta-data, user-data, vendor-data and network-config
  LXD exposes through the devlxd socket and apply them with nuageinit(7).

  The configuration is applied once per cloud-init instance ID; the ID of the
  last applied configuration is kept in the state directory.
`,
		RunE: c.run,
	}

	cmd.Flags().StringVar(&c.socket, "socket", "/var/run/lxd/sock", "devlxd unix socket")
	cmd.Flags().StringVar(&c.stateDir, "state-dir", "/var/db/lxd-agent", "where the applied instance ID is recorded")
	cmd.Flags().StringVar(&c.seedDir, "seed-dir", "/var/cache/lxd-cloudinit/seed", "where the NoCloud seed is written")
	cmd.Flags().StringVar(&c.runDir, "run-dir", "/var/run/lxd-cloudinit", "where the 'applied' marker for this boot is written")
	cmd.Flags().StringVar(&c.nuageinit, "nuageinit", "/usr/libexec/nuageinit", "path to nuageinit")
	cmd.Flags().DurationVar(&c.timeout, "timeout", 60*time.Second, "how long to wait for the devlxd socket")
	cmd.Flags().BoolVar(&c.force, "force", false, "apply even if this instance ID was already applied")

	return cmd
}

// instanceID extracts instance-id from cloud-init meta-data.
func instanceID(metaData string) string {
	scanner := bufio.NewScanner(strings.NewReader(metaData))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), ":")
		if ok && strings.TrimSpace(key) == "instance-id" {
			return strings.TrimSpace(value)
		}
	}

	return ""
}

// firstConfig returns the value of the first key that is set.
func firstConfig(ctx context.Context, client *devlxd.Client, keys []string, candidates ...string) (string, string, error) {
	for _, key := range candidates {
		if !slices.Contains(keys, key) {
			continue
		}

		value, err := client.Config(ctx, key)
		if err != nil {
			return "", "", err
		}

		if strings.TrimSpace(value) != "" {
			return key, value, nil
		}
	}

	return "", "", nil
}

// networkConfig converts a cloud-init network-config into the form nuageinit's
// NoCloud mode expects: a netplan style (version 2) document without the
// top-level "network" key. Unsupported documents yield "" and a reason.
func networkConfig(raw string) (string, string) {
	var doc map[string]any
	err := yaml.Unmarshal([]byte(raw), &doc)
	if err != nil || doc == nil {
		return "", "network-config is not a YAML mapping"
	}

	inner, ok := doc["network"].(map[any]any)
	if ok {
		converted := make(map[string]any, len(inner))
		for k, v := range inner {
			ks, ok := k.(string)
			if ok {
				converted[ks] = v
			}
		}

		doc = converted
	}

	if _, ok := doc["ethernets"]; !ok {
		return "", "network-config has no 'ethernets' section (only version 2 network-config is supported by nuageinit)"
	}

	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", err.Error()
	}

	return string(out), ""
}

func (c *cmdCloudInit) run(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	client := devlxd.New(c.socket)

	err := client.Wait(ctx)
	if err != nil {
		return err
	}

	metaData, err := client.MetaData(ctx)
	if err != nil {
		return fmt.Errorf("Failed fetching meta-data: %w", err)
	}

	id := instanceID(metaData)
	if id == "" {
		return errors.New("meta-data has no instance-id")
	}

	statePath := filepath.Join(c.stateDir, "cloud-init.instance-id")
	previous, err := os.ReadFile(statePath)
	if err == nil && strings.TrimSpace(string(previous)) == id && !c.force {
		fmt.Printf("cloud-init: instance %s already applied, nothing to do\n", id)
		return nil
	}

	keys, err := client.ConfigKeys(ctx)
	if err != nil {
		return fmt.Errorf("Failed listing config keys: %w", err)
	}

	userKey, userData, err := firstConfig(ctx, client, keys, "cloud-init.user-data", "user.user-data")
	if err != nil {
		return fmt.Errorf("Failed fetching user-data: %w", err)
	}

	vendorKey, vendorData, err := firstConfig(ctx, client, keys, "cloud-init.vendor-data", "user.vendor-data")
	if err != nil {
		return fmt.Errorf("Failed fetching vendor-data: %w", err)
	}

	networkKey, networkData, err := firstConfig(ctx, client, keys, "cloud-init.network-config", "user.network-config")
	if err != nil {
		return fmt.Errorf("Failed fetching network-config: %w", err)
	}

	// nuageinit has no notion of vendor-data, and no way to merge two documents.
	switch {
	case userData != "":
		fmt.Printf("cloud-init: using %s\n", userKey)
		if vendorData != "" {
			fmt.Printf("cloud-init: ignoring %s (nuageinit cannot merge it with user-data)\n", vendorKey)
		}

	case vendorData != "":
		fmt.Printf("cloud-init: using %s as user-data\n", vendorKey)
		userData = vendorData
	default:
		// Without user-data nuageinit creates its default "freebsd" user; an
		// empty users list keeps the hostname handling but not the user.
		userData = "#cloud-config\nusers: []\n"
	}

	// Write the NoCloud seed.
	err = os.RemoveAll(c.seedDir)
	if err != nil {
		return err
	}

	err = os.MkdirAll(c.seedDir, 0700)
	if err != nil {
		return err
	}

	err = os.WriteFile(filepath.Join(c.seedDir, "meta-data"), []byte(metaData), 0600)
	if err != nil {
		return err
	}

	err = os.WriteFile(filepath.Join(c.seedDir, "user-data"), []byte(userData), 0600)
	if err != nil {
		return err
	}

	if networkData != "" {
		converted, reason := networkConfig(networkData)
		if converted == "" {
			fmt.Printf("cloud-init: ignoring %s: %s\n", networkKey, reason)
		} else {
			fmt.Printf("cloud-init: using %s\n", networkKey)
			err = os.WriteFile(filepath.Join(c.seedDir, "network-config"), []byte(converted), 0600)
			if err != nil {
				return err
			}
		}
	}

	// Clear what a previous run left for the post-network stages.
	for _, stale := range []string{"/var/cache/nuageinit/user_data", "/var/cache/nuageinit/user-data", "/var/cache/nuageinit/runcmds"} {
		_ = os.Remove(stale)
	}

	fmt.Printf("cloud-init: applying instance %s with nuageinit\n", id)
	nuage := exec.Command(c.nuageinit, c.seedDir, "nocloud")
	nuage.Stdout = os.Stdout
	nuage.Stderr = os.Stderr
	err = nuage.Run()
	if err != nil {
		return fmt.Errorf("nuageinit failed: %w", err)
	}

	err = os.MkdirAll(c.stateDir, 0755)
	if err != nil {
		return err
	}

	err = os.WriteFile(statePath, []byte(id+"\n"), 0644)
	if err != nil {
		return err
	}

	err = os.MkdirAll(c.runDir, 0755)
	if err != nil {
		return err
	}

	// Tell the post-network rc scripts that there is work for them this boot.
	return os.WriteFile(filepath.Join(c.runDir, "applied"), []byte(id+"\n"), 0644)
}
