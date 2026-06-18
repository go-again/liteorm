#!/usr/bin/env python3
"""Pin every intra-repo liteorm.org* module require to one version, across all
workspace modules. Use before tagging a release, e.g.:

    dev/pin.py v0.9.0 && go work sync     (or: just pin v0.9.0)

Only `liteorm.org`/`liteorm.org/...` requires are touched (the `// indirect`
markers and third-party + gosqlite.org versions are left alone). The relative
`replace` directives are path-based, so they need no change on a version bump.
"""
import os, re, sys

if len(sys.argv) != 2 or not re.fullmatch(r"v\d+\.\d+\.\d+(-\S+)?", sys.argv[1]):
    sys.exit("usage: dev/pin.py vX.Y.Z")
ver = sys.argv[1]
root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
mods = re.findall(r"^\s+(\.(?:/\S+)?)\s*$", open(os.path.join(root, "go.work")).read(), re.M)

changed = 0
for m in mods:
    gm = os.path.join(root, m, "go.mod")
    if not os.path.exists(gm):
        continue
    src = open(gm).read()
    out = re.sub(r"(\bliteorm\.org\S*) v\d\S*", lambda x: f"{x.group(1)} {ver}", src)
    if out != src:
        open(gm, "w").write(out)
        changed += 1
print(f"pinned liteorm.org* -> {ver} in {changed} module(s); now run `go work sync`")
