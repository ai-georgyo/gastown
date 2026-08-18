+++
name = "rebuild-gt"
description = "Rebuild stale gt binary from gastown source"
version = 2

[gate]
type = "cooldown"
duration = "1h"

[tracking]
labels = ["plugin:rebuild-gt", "rig:gastown", "category:maintenance"]
digest = true

[execution]
timeout = "5m"
notify_on_failure = true
severity = "medium"
+++

# Rebuild gt Binary

Checks if the gt binary is stale (built from older commit than HEAD) and rebuilds.

**SAFETY**: This plugin MUST only rebuild forward (binary ancestor of HEAD) and
only from the main branch. Rebuilding to an older or diverged commit caused a
crash loop where every new session's startup hook failed, the witness respawned
it, and the loop repeated every 1-2 minutes.

## Gate Check

The Deacon evaluates this before dispatch. If gate closed, skip.

## Detection

Check binary staleness. Invoke the **installed** gt explicitly rather than
whatever `gt` resolves to: a long-lived session pins the store path it started
with, so `gt` on PATH can be superseded code reporting on itself, answering
"stale" after every successful rebuild (gt-3pk).

```bash
GT="$HOME/.nix-profile/bin/gt"   # or $HOME/.local/bin/gt
"$GT" stale --json
```

Parse the JSON output and check these fields:
- If `"stale": false` → record success wisp and exit early (binary is fresh)
- If `"safe_to_rebuild": false` → **DO NOT REBUILD**. Record a skip wisp and exit.
  This means the repo is on a non-main branch or HEAD is not a descendant of the
  binary commit (would be a downgrade).
- If `"safe_to_rebuild": true` → proceed to build
- `"superseded": true` means the gt that produced this JSON is not the installed
  binary. Rebuilding will not change what that session runs — the session has to
  be restarted. Re-run through the installed path before acting on the verdict.

If `safe_to_rebuild` is false, record a skip wisp:
```bash
gt plugin record-run --plugin rebuild-gt --result skipped --rig gastown \
  --title "Plugin: rebuild-gt [skipped]" \
  --description "Skipped: not safe to rebuild (forward=$FORWARD, main=$ON_MAIN)" >/dev/null 2>&1 || true
```

## Pre-flight Checks

Before building, verify the source repo is clean and on main:

```bash
cd ~/gt/gastown/mayor/rig
git status --porcelain  # Must be clean
git branch --show-current  # Must be "main"
```

If either check fails, skip the rebuild and record a wisp.

## Action

Rebuild from source (the mayor/rig directory is the canonical source):

```bash
cd ~/gt/gastown/mayor/rig && make build && make safe-install
```

**IMPORTANT**: Use `make safe-install` (not `make install`) to avoid restarting
the daemon while sessions are active. safe-install replaces the binary but does
NOT restart the daemon — sessions will pick up the new binary on their next cycle.

**Installing is not deploying.** Long-lived sessions keep executing the binary
they started with; nothing re-execs them. After a rebuild, check which sessions
are still on superseded code and restart them:

```bash
"$GT" stale        # lists live gt processes not running the installed binary
"$GT" doctor       # same finding as the superseded-binary check
```

## Record Result

On success:
```bash
gt plugin record-run --plugin rebuild-gt --result success --rig gastown \
  --title "Plugin: rebuild-gt [success]" \
  --description "Rebuilt gt: $OLD → $NEW ($N commits)" >/dev/null 2>&1 || true
```

On failure:
```bash
gt plugin record-run --plugin rebuild-gt --result failure --rig gastown \
  --title "Plugin: rebuild-gt [failure]" \
  --description "Build failed: $ERROR" >/dev/null 2>&1 || true

gt escalate --severity=medium \
  --subject="Plugin FAILED: rebuild-gt" \
  --body="$ERROR" \
  --source="plugin:rebuild-gt"
```
