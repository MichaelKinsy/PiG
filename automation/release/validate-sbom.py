#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Validate structural SBOM integrity and emit a review inventory.

This check does not decide licenses or vulnerability applicability. It makes
missing identities and unresolved license fields visible for human review.
"""

from __future__ import annotations

import argparse
import csv
import hashlib
import json
from pathlib import Path
from typing import Any


def load_json(path: Path) -> dict[str, Any]:
    with path.open(encoding="utf-8") as stream:
        value = json.load(stream)
    if not isinstance(value, dict):
        raise ValueError(f"{path}: root must be an object")
    return value


def file_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def validate_spdx(document: dict[str, Any]) -> tuple[list[dict[str, str]], list[str]]:
    if document.get("spdxVersion") != "SPDX-2.3":
        raise ValueError("SPDX document must use SPDX-2.3")
    namespace = document.get("documentNamespace")
    if not isinstance(namespace, str) or not namespace:
        raise ValueError("SPDX documentNamespace is required")
    packages = document.get("packages")
    if not isinstance(packages, list) or not packages:
        raise ValueError("SPDX document must contain packages")

    rows: list[dict[str, str]] = []
    unresolved: list[str] = []
    seen: set[str] = set()
    for package in packages:
        if not isinstance(package, dict):
            raise ValueError("SPDX package entry must be an object")
        spdx_id = package.get("SPDXID")
        name = package.get("name")
        if not isinstance(spdx_id, str) or not spdx_id:
            raise ValueError("SPDX package is missing SPDXID")
        if spdx_id in seen:
            raise ValueError(f"duplicate SPDXID: {spdx_id}")
        seen.add(spdx_id)
        if not isinstance(name, str) or not name:
            raise ValueError(f"{spdx_id}: package name is required")
        declared = str(package.get("licenseDeclared", "NOASSERTION"))
        concluded = str(package.get("licenseConcluded", "NOASSERTION"))
        if declared == "NOASSERTION" and concluded == "NOASSERTION":
            unresolved.append(spdx_id)
        purls = []
        for reference in package.get("externalRefs", []):
            if isinstance(reference, dict) and reference.get("referenceType") == "purl":
                locator = reference.get("referenceLocator")
                if isinstance(locator, str):
                    purls.append(locator)
        rows.append(
            {
                "spdx_id": spdx_id,
                "name": name,
                "version": str(package.get("versionInfo", "")),
                "supplier": str(package.get("supplier", "")),
                "license_declared": declared,
                "license_concluded": concluded,
                "download_location": str(package.get("downloadLocation", "")),
                "purl": " ".join(sorted(purls)),
                "review_status": "review-required" if spdx_id in unresolved else "scanner-resolved",
            }
        )

    relationship_ids = {"SPDXRef-DOCUMENT", *seen}
    for collection in (document.get("files", []), document.get("snippets", [])):
        if not isinstance(collection, list):
            raise ValueError("SPDX files and snippets must be arrays")
        for element in collection:
            if not isinstance(element, dict):
                raise ValueError("SPDX file or snippet entry must be an object")
            element_id = element.get("SPDXID")
            if not isinstance(element_id, str) or not element_id:
                raise ValueError("SPDX file or snippet is missing SPDXID")
            if element_id in relationship_ids:
                raise ValueError(f"duplicate SPDXID: {element_id}")
            relationship_ids.add(element_id)
    for relationship in document.get("relationships", []):
        if not isinstance(relationship, dict):
            raise ValueError("SPDX relationship must be an object")
        source = relationship.get("spdxElementId")
        target = relationship.get("relatedSpdxElement")
        for value in (source, target):
            if (
                isinstance(value, str)
                and value.startswith("SPDXRef-")
                and value not in relationship_ids
            ):
                raise ValueError(f"SPDX relationship references unknown element: {value}")
    return rows, unresolved


def validate_cyclonedx(document: dict[str, Any]) -> int:
    if document.get("bomFormat") != "CycloneDX":
        raise ValueError("CycloneDX document has the wrong bomFormat")
    if document.get("specVersion") not in {"1.5", "1.6", "1.7"}:
        raise ValueError("CycloneDX document uses an unsupported specVersion")
    components = document.get("components")
    if not isinstance(components, list) or not components:
        raise ValueError("CycloneDX document must contain components")
    refs: set[str] = set()
    for component in components:
        if not isinstance(component, dict):
            raise ValueError("CycloneDX component must be an object")
        name = component.get("name")
        ref = component.get("bom-ref")
        if not isinstance(name, str) or not name:
            raise ValueError("CycloneDX component name is required")
        if isinstance(ref, str):
            if ref in refs:
                raise ValueError(f"duplicate CycloneDX bom-ref: {ref}")
            refs.add(ref)
    return len(components)


def manifest_inventory(root: Path) -> list[dict[str, str]]:
    names = {
        "go.mod",
        "go.sum",
        "go.work",
        "go.work.sum",
        "package.json",
        "package-lock.json",
        "Cargo.toml",
        "Cargo.lock",
        "pyproject.toml",
        "uv.lock",
    }
    records = []
    for path in sorted(p for p in root.rglob("*") if p.is_file() and p.name in names and ".git" not in p.parts):
        records.append({"path": path.relative_to(root).as_posix(), "sha256": file_sha256(path)})
    return records


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--spdx", type=Path, required=True)
    parser.add_argument("--cyclonedx", type=Path, required=True)
    parser.add_argument("--root", type=Path, default=Path("."))
    parser.add_argument("--licenses", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--require-complete-licenses", action="store_true")
    args = parser.parse_args()

    rows, unresolved = validate_spdx(load_json(args.spdx))
    cyclonedx_count = validate_cyclonedx(load_json(args.cyclonedx))
    if args.require_complete_licenses and unresolved:
        raise SystemExit(f"{len(unresolved)} SPDX packages require license review")

    args.licenses.parent.mkdir(parents=True, exist_ok=True)
    with args.licenses.open("w", encoding="utf-8", newline="") as stream:
        writer = csv.DictWriter(stream, fieldnames=list(rows[0]))
        writer.writeheader()
        writer.writerows(sorted(rows, key=lambda row: (row["name"], row["version"], row["spdx_id"])))

    report = {
        "spdx": {
            "path": args.spdx.as_posix(),
            "sha256": file_sha256(args.spdx),
            "packages": len(rows),
            "licenseReviewRequired": len(unresolved),
        },
        "cyclonedx": {
            "path": args.cyclonedx.as_posix(),
            "sha256": file_sha256(args.cyclonedx),
            "components": cyclonedx_count,
        },
        "manifests": manifest_inventory(args.root.resolve()),
        "validation": "structural",
        "humanReviewRequired": bool(unresolved),
    }
    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(f"SPDX packages: {len(rows)}")
    print(f"CycloneDX components: {cyclonedx_count}")
    print(f"SPDX license review required: {len(unresolved)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
