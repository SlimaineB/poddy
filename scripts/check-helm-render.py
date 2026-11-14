#!/usr/bin/env python3
"""Fail when a chart renders merged YAML documents.

Helm concatenates the output of every `include` in a template file. If a
helper leaves indentation behind, the `---` separator that should start the
next document ends up indented and YAML silently keeps the last document,
dropping the others. This check renders the templates chart in the modes the
chart supports and asserts one top-level ConfigMap per document.
"""

import subprocess
import sys

CHART = "deploy/helm/poddy-templates"
RELEASE = "poddy-templates"
NAMESPACE = "poddy-workspaces"


def render(extra):
    cmd = ["helm", "template", RELEASE, CHART, "-n", NAMESPACE, *extra]
    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        sys.exit(f"helm template failed: {result.stderr.strip()}")
    return result.stdout


def split_documents(output):
    documents = []
    current = []
    for line in output.splitlines():
        if line == "---":
            if current:
                documents.append("\n".join(current))
                current = []
            continue
        current.append(line)
    if current:
        documents.append("\n".join(current))
    return [doc for doc in documents if doc.strip()]


def configmap_names(output):
    names = []
    for document in split_documents(output):
        top_level = [line for line in document.splitlines() if line == "kind: ConfigMap"]
        if len(top_level) != 1:
            snippet = "\n".join(document.splitlines()[:8])
            sys.exit(
                f"expected exactly one top-level ConfigMap per document, found {len(top_level)}:\n{snippet}"
            )
        name = None
        for line in document.splitlines():
            if line.startswith("  name: "):
                name = line.split(": ", 1)[1].strip()
                break
        if not name:
            sys.exit(f"ConfigMap without metadata.name:\n{document[:200]}")
        names.append(name)
    return names


def expect(label, output, want):
    got = configmap_names(output)
    if got != want:
        sys.exit(f"{label}: ConfigMaps = {got}, want {want}")


def main():
    expect(
        "defaults",
        render([]),
        [
            "poddy-templates-base",
            "poddy-templates-go",
            "poddy-templates-node",
            "poddy-templates-python",
        ],
    )
    expect("builtins disabled", render(["--set", "builtins.enabled=false"]), [])
    expect(
        "custom template",
        render(
            [
                "--set",
                "builtins.enabled=false",
                "--set",
                "templates.custom.template=name: custom\nimage: example/image:1",
                "--set",
                "templates.custom.pod=apiVersion: v1\nkind: Pod\nspec: {}",
            ]
        ),
        ["poddy-templates-custom"],
    )
    print("helm render OK")


if __name__ == "__main__":
    main()
