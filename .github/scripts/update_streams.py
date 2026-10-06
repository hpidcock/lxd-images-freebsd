#!/usr/bin/env python3
"""Updates streams/v1/images.json and streams/v1/index.json for the FreeBSD
LXD simplestreams remote.

For each --manifest given (the <name>.json written by image/build.sh), this
upserts a product in images.json keyed freebsd:<release>:<arch>:<variant>
and adds (or replaces, if one already exists for the same release tag) a
"version" entry whose items point at the release assets uploaded alongside
this script's output.

Each version carries LXD's split virtual-machine image format: an
"lxd.tar.xz" item (metadata tarball) holding combined_disk-kvm-img_sha256,
and a "disk-kvm.img" item (the qcow2 root disk). LXD's simplestreams client
(shared/simplestreams/products.go) uses the combined hash as the image
fingerprint and the disk-kvm.img item type to mark the image as a VM.

Item paths are relative to the simplestreams remote's base URL (e.g.
"v1.0.0/freebsd-15.1-amd64-default.disk.qcow2"). A GitHub release asset
lives at:

    https://github.com/<owner>/<repo>/releases/download/<tag>/<asset>

so adding the remote as:

    lxc remote add freebsd https://github.com/<owner>/<repo>/releases/download --protocol simplestreams

makes those relative paths resolve to the correct download URLs.
"""

from __future__ import annotations

import argparse
import datetime
import json
import os
import sys
from typing import Any

FORMAT = "products:1.0"


def load_images(path: str | None) -> dict[str, Any]:
    if path and os.path.exists(path):
        with open(path) as f:
            data = json.load(f)

        data.setdefault("products", {})
        return data

    return {
        "content_id": "images",
        "format": FORMAT,
        "datatype": "image-downloads",
        "products": {},
    }


def upsert_version(
    product: dict[str, Any],
    tag: str,
    items: dict[str, Any],
    timestamp: str,
) -> None:
    for version in product["versions"].values():
        if version.get("label") == tag:
            version["items"] = items
            return

    # LXD parses the version key as the image creation date, so it must be a
    # YYYYMMDD_HHMM timestamp and unique within the product.
    key = timestamp
    while key in product["versions"]:
        key += "0"

    product["versions"][key] = {"label": tag, "items": items}


def aliases(release: str, variant: str) -> str:
    names = [f"freebsd/{release}/{variant}", f"{release}/{variant}"]
    if variant == "default":
        names += [f"freebsd/{release}", release]

    return ",".join(names)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True, help="Release tag, e.g. v1.0.0")
    parser.add_argument(
        "--existing-images", help="Path to a previously published images.json, if any"
    )
    parser.add_argument("--output-images", required=True)
    parser.add_argument("--output-index", required=True)
    parser.add_argument(
        "--manifest",
        action="append",
        default=[],
        metavar="PATH",
        help="Manifest JSON written by image/build.sh (repeatable)",
    )
    args = parser.parse_args()

    if not args.manifest:
        print("error: at least one --manifest is required", file=sys.stderr)
        return 1

    images = load_images(args.existing_images)

    now = datetime.datetime.now(datetime.timezone.utc)
    timestamp = now.strftime("%Y%m%d_%H%M")

    for path in args.manifest:
        with open(path) as f:
            manifest = json.load(f)

        release = manifest["release"]
        arch = manifest["arch"]
        variant = manifest["variant"]

        product_key = f"freebsd:{release}:{arch}:{variant}"
        product = images["products"].setdefault(
            product_key,
            {
                "aliases": aliases(release, variant),
                "arch": arch,
                "distro": "freebsd",
                "os": "FreeBSD",
                "release": release,
                "release_title": release,
                "variant": variant,
                # FreeBSD's loader is not signed for UEFI Secure Boot; LXD
                # turns this into the image property requirements.secureboot.
                "requirements": {"secureboot": "false"},
                "versions": {},
            },
        )
        product.setdefault("versions", {})

        items = {}
        for key, item in manifest["items"].items():
            entry = {
                "ftype": item["ftype"],
                "path": f"{args.version}/{item['file']}",
                "size": item["size"],
                "sha256": item["sha256"],
            }
            if "combined_disk-kvm-img_sha256" in item:
                entry["combined_disk-kvm-img_sha256"] = item[
                    "combined_disk-kvm-img_sha256"
                ]

            items[key] = entry

        if "lxd.tar.xz" not in items or "disk-kvm.img" not in items:
            print(f"error: {path} lacks lxd.tar.xz/disk-kvm.img items", file=sys.stderr)
            return 1

        upsert_version(product, args.version, items, timestamp)

    images["updated"] = now.strftime("%Y-%m-%dT%H:%M:%SZ")

    os.makedirs(os.path.dirname(args.output_images) or ".", exist_ok=True)
    with open(args.output_images, "w") as f:
        json.dump(images, f, indent=2)
        f.write("\n")

    index = {
        "format": "index:1.0",
        "index": {
            "images": {
                "datatype": "image-downloads",
                "path": "streams/v1/images.json",
                "format": FORMAT,
                "updated": images["updated"],
                "products": sorted(images["products"].keys()),
            }
        },
    }

    os.makedirs(os.path.dirname(args.output_index) or ".", exist_ok=True)
    with open(args.output_index, "w") as f:
        json.dump(index, f, indent=2)
        f.write("\n")

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
