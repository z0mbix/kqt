# kqt

**A Kubernetes Query Tool** — a keyboard-first Kubernetes browser with live
resource tables, manifest inspection, logs, workload actions, and customisable
layouts and themes.

kqt uses Bubble Tea v2, Bubbles v2, Lip Gloss v2, and the official Kubernetes
client-go. It talks directly to Kubernetes;
kubectl is not required at runtime.

![kqt resource explorer with highlighted YAML](docs/screenshots/explorer.png)

## Install on macOS

Install the latest stable release on Apple Silicon or Intel Macs with Homebrew:

```sh
brew install z0mbix/tap/kqt
```

Update with `brew update && brew upgrade kqt`. The
[tap](https://github.com/z0mbix/homebrew-tap) installs the prebuilt release binary;
Go and kubectl are not required.

## Run

Requires Go 1.27.2 or later; mise pins Go 1.27.2 for this repository.
With mise activated in your shell, the repository's mise configuration adds
`./bin/` to your `PATH` while you work here, so you can run the built binary as
`kqt`.

```sh
go build -trimpath -o bin/kqt ./cmd/kqt
kqt --demo                      # Offline sample cluster
kqt                             # Current context and saved view / namespace
kqt -A                          # Override saved scope: all namespaces
kqt -n prod-dep,prod-ccp          # Select several namespaces
kqt --context staging -n apps --theme nord
kqt --view deploy --layout classic -l app=web
```

With mise and just, use `just build`, `just demo`, and `just check`.

## Binary builds

The **Build binaries** GitHub workflow runs on pushes (including tags), pull
requests, and manual dispatch. Pushing a [SemVer](https://semver.org/) tag such as
`v0.2.0` creates a [GitHub release](https://github.com/z0mbix/kqt/releases) after
the tests and all six builds pass. Tags with prerelease identifiers, such as
`v0.2.0-rc.1`, create prereleases. Invalid version tags fail before building.
Download the matching release archive, or use the workflow run's **Artifacts**
section for development builds:

| Platform | Artifact |
| --- | --- |
| macOS Intel | `kqt-darwin-amd64` |
| macOS Apple Silicon | `kqt-darwin-arm64` |
| Linux x86-64 | `kqt-linux-amd64` |
| Linux ARM64 | `kqt-linux-arm64` |
| FreeBSD x86-64 | `kqt-freebsd-amd64` |
| FreeBSD ARM64 | `kqt-freebsd-arm64` |

Each artifact contains a `.tar.gz` archive and its `.sha256` checksum file. The
archive includes the executable, README, and theme license notices. Extract it
and run the `kqt` executable inside, or copy it onto your PATH. Artifacts are kept
for 30 days; release downloads remain attached to their version. Releases also
include a combined `SHA256SUMS` file. From the download directory, verify the
chosen archive with `shasum -a 256 -c kqt-darwin-arm64.tar.gz.sha256` (adjust the
platform as needed).
Tag builds report the tag as their version; other builds report `dev-<commit>`.
All six targets are cross-compiled on Linux with CGO disabled.

After a stable release is published, the workflow verifies the published macOS
archives against `SHA256SUMS` and updates `Formula/kqt.rb` in
`z0mbix/homebrew-tap`. Prereleases leave the tap on the stable version. The
**Publish Homebrew formula** workflow can also be dispatched with an existing
latest stable tag to retry publishing without rebuilding or creating a release.

Publishing uses the `HOMEBREW_TAP_SSH_KEY` Actions secret in this repository,
paired with a write-enabled deploy key on the tap. To rotate it, generate a new
dedicated SSH key, register its public key on the tap, replace this secret with
the private key, then remove the old deploy key. Each additional software
repository should use its own deploy key and secret for this shared tap.

## Kubernetes connection

`--kubeconfig` selects one kubeconfig file. Otherwise Kubernetes's normal
`KUBECONFIG` merging or `~/.kube/config` loading applies. `--context` overrides
current-context for this session; kqt never rewrites kubeconfig. Exec credential
plugins and OIDC are supported by client-go. Without a current context, a context
picker opens. UI preferences never silently select a different Kubernetes context.

Use `:alias prod` to give the current context a short name, or
`:alias prod "actual-context-name"` to name another configured context.
`:ctx prod` and `kqt --context prod` resolve the alias. The header uses the short
name; the context picker also shows the real name. Picker rows omit repeated
context/cluster names and label namespace and distinct cluster details.
Additional aliases remain visible and searchable. `:alias` lists aliases and
`:unalias prod` removes one. Aliases live in kqt's config and leave kubeconfig
unchanged. Saved views and namespaces stay attached to the real context name.
Picker names use the header's theme colours, including production-context
highlighting, with readable emphasis on selected rows and in the no-colour theme.

The single-line header shows the context, namespace and connection state, with
the kqt title on the right (shortened to `kqt` on narrower terminals). Context
names containing `prod`, `production` or `prd` are highlighted in the theme's
error colour. Matching ignores case and excludes `nonprod`, `non-prod`,
`non-production`, `nonprd`, `preprod`, `preprd`, `pre-prod`, `pre-production` and
`pre-prd` (also their underscore/dot variants). Both the real context name and
its alias are checked; a separate production marker still takes precedence.
The `no-color` theme uses reverse video and underlining for production contexts.

## Explore

Shortcut bars and the `?` / `:help` view highlight keys in the active theme's
accent colour, with muted descriptions: `j/k: move`, `Tab: inspector`, `Esc: close`.
Help shows shortcuts on the left and scrollable notes on the right. `Tab` switches
keyboard focus between them; the mouse wheel scrolls the pane under the pointer.
Below 80 columns, `Tab` switches which pane is shown.

In help, press `/` for live fuzzy search across shortcuts, descriptions, sections
and notes. Enter keeps the filtered results for scrolling; Escape cancels the
search and restores the previous view. Clear the query to show all help, or press
Escape outside search to close it. Reopening help starts with the full view.

| Key | Action |
| --- | --- |
| `j/k`, arrows; `g/G` | Move, jump to first / last row |
| `Ctrl+F/B`, `PageDown/PageUp` | Full page down / up in lists and focused panels |
| `Ctrl+D/U` | Half page down / up in lists and focused panels |
| `h/l`, arrows | Scroll wide tables or manifests |
| `:`; `Tab` / `Shift+Tab` | Command prompt; cycle completions forward / back |
| `Up` / `Down` in `:` | Recall successful commands (100 by default) |
| `Ctrl+R` in `:` | Reverse search history; repeat for older matches |
| `/` | Fuzzy search; `web0003` matches `web-7c9f6d8b5-0003` |
| `n`, `:ns` | Namespace table; Enter selects one |
| `:ns apps`, `:ns -A`, `0` | Set namespace / all namespaces |
| `:ns apps,platform` | List resources from several namespaces |
| `:ns @` | Search and manage saved namespace groups for this context |
| `:ns @save production`, `:ns @production` | Save / select a namespace group |
| `:ns @edit NAME`, `:ns @rename OLD NEW`, `:ns @delete NAME` | Edit, rename or delete a saved group |
| `c`, `:ctx` | Search and switch kubeconfig contexts |
| `r`, `:resources` | Search APIs and discovered custom resources |
| `Enter` | Workload/service/node → pods; otherwise describe |
| `b`, `:related` | Explore owner, backend, pod and storage relationships |
| `C`, `o`, `:copy`, `:export` | Copy or privately save YAML/JSON; optional clean form |
| `a`, `:actions`; `D`, `:delete` | Confirm a workload action or resource deletion |
| `A`, `:permissions`, `:can-i` | Inspect current identity and permissions |
| `U`, `:overview` | Cluster health, capacity, usage and warnings |
| `H`, `:rollouts` | Deployment revision history, images and template comparison |
| `:rollback REVISION` | Confirm restoring a retained Deployment pod template |
| `d`, `y` / `v`, `J` | Describe, highlighted YAML, highlighted JSON |
| `Tab`, then `J` / `y` | Focus inspector, then switch it to JSON / YAML |
| `/` in the focused inspector | Fuzzy search YAML/JSON without hiding document lines |
| `n` / `N` in the inspector | Next / previous matching line; `Esc` clears the search |
| `x` on a Secret | Toggle decoded values in the inspector or details |
| `L`, `P` | Live logs / previous log snapshots from a pod, deployment, or statefulset |
| `w`, `s`, `f` in logs | Toggle line wrapping, follow latest lines, logs-only terminal |
| `Space`, `/`, `F`, `o` in logs | Pause, search, switch live/snapshot, save displayed logs |
| `O`, `T`, `N` in logs | Saved log options, toggle timestamps, toggle source prefixes |
| `H` in logs | Toggle syntax highlighting; saved between sessions |
| Mouse drag | Select visible text; release to copy, with a brief “Copied” flash |
| `S` (also `E`), `:status`, `:events selected` | Readable status, conditions, containers, and live events |
| `M`, `:metadata` | Resource labels, annotations and finalizers in the same panel |
| `5` in Pod/Service status, `:diagnose` | Scheduling diagnostics or Service backend troubleshooting |
| `:bundle [--logs] [PATH]` | Save a private diagnostic archive; logs are opt-in |
| `m`, `:metrics on` / `off` | Toggle pod/node CPU and memory columns |
| `s`, `:shell` | Open a shell in a running pod/container |
| `B`, `:debug [PROFILE]` | Preview, confirm and attach an ephemeral debug container |
| `:debug attach [NAME]` | Reattach an existing ephemeral debug container |
| `f`, `:forward LOCAL:REMOTE` | Forward a pod/service port to localhost |
| `x` on a pod/service | Stop forwards associated with the selected resource |
| `F`, `:forwards` | View/manage forwards; `x` stops the selected forward |
| `e`, `:edit` | Open the selected resource in your editor |
| `t`, `:themes` | Fuzzy theme picker with reversible previews |
| `:layouts`, `:layout NAME` | Choose a saved layout |
| `:write NAME` | Save current arrangement, columns, sort, and wide state |
| `i` | Toggle the inspector off/on, remembering its position |
| `z`, `:header [on\|off\|toggle]` | Toggle the single-line explorer header; saved between sessions |
| `I` (Shift+i), `:inspector` | Choose inspector position: right or bottom |
| `w` | Toggle wide fields |
| `O` | Sort by the next visible column, ascending; wraps to the first |
| `Shift+Left/Right` or `[` / `]` | Sort by the previous / next visible column |
| `R` | Reverse current sort direction |
| `:sort`, `:sort COLUMN asc` / `desc` | Choose a column or set its order explicitly |
| `Ctrl+R`, `Escape`, `?`, `q` | Refresh, back/close, help, quit |

`i` only changes inspector visibility. `I` opens a compact placement chooser.
Move with arrows, Ctrl+J/K or the mouse wheel, or type to filter: the highlighted
position previews immediately, including when the inspector was hidden.
Enter saves the position and shows the inspector; Escape restores the previous
visibility, position and scroll offsets. Placement previews are not saved.
Both settings are saved on confirmation, and toggling keeps
the active layout's columns, sorting and pane size. `:inspector on`, `off`, and
`toggle` control visibility; `:inspector right` or `bottom` places it directly
(`none` remains an alias for `off`). On smaller terminals the inspector temporarily
uses the other position if it fits, returning to your chosen position after a
resize. Selecting a saved layout applies that layout's visibility and position;
`:write NAME` captures the current inspector arrangement.

Hiding the header gives its row to the resource list and inspector, or to
the open logs/status view. The status and shortcut bars remain available. Press
`z` again to restore it, or set `header_visible: false` in your config.

Focus the inspector with `Tab` or a mouse click, then press `/` to search its
YAML or JSON. Matching is case-insensitive and fuzzy: `contport` can find
`containerPort`. Matches highlight as you type and the best matching line is
brought into view, while the full document stays visible. The inspector title
shows the current match and total matching lines, or `no matches`.
Use arrows while typing, or `n` / `N` after `Enter`, to cycle through matching
lines in document order. `Enter` keeps the search; `/` edits it; `Esc` while
typing restores the previous search and scroll position. Outside the prompt,
`Esc` clears the search, then another `Esc` returns focus to the list.
Switching YAML/JSON keeps the query. Selecting a different resource clears it.
Inspector searches are never saved to config or command history; hidden Secret
values remain hidden and cannot match.

Completion covers commands, resource names and aliases, theme/layout names,
contexts, known namespaces, saved namespace groups and their `@` actions, and
inspector/sort options. Tab only fills the prompt; Enter executes. Printable
navigation keys stay text in input fields.
Pickers use arrows or `Ctrl+J/K` while you type to search.

In the command prompt, Up recalls older commands and Down recalls newer ones.
Down past the newest entry restores your unfinished command and cursor position.
Recalling a command only fills the prompt; Enter executes it. By default, the last
100 successful commands are saved in a separate `history.json` cache file and remain
available after restarting. With `XDG_CACHE_HOME` set, the path is
`$XDG_CACHE_HOME/kqt/history.json`; otherwise kqt uses
`~/.local/cache/kqt/history.json` on macOS and `~/.cache/kqt/history.json` on
Linux/FreeBSD. History files are private (`0600`). Existing `command_history`
entries in YAML/TOML config files are migrated automatically when the TUI starts;
the old field is removed only after the cache is saved. Deleting the cache while
kqt is closed clears history. Config validation does not read or migrate history.

Set `history_limit: 500` in your config file to retain more commands (or choose a
smaller positive integer). The default is `100`. The same limit applies to saved
history, arrow-key recall and `Ctrl+R` search. After reducing the limit, kqt keeps
the newest entries when loading history and on the next save.

Typos, invalid arguments, failed requests, cancelled
operations and search text are not recorded. Requests enter history after
success, retaining the order in which you submitted them.

Press `Ctrl+R` in the `:` prompt for incremental, case-insensitive history search.
Type part of a command; `Ctrl+R` or Up finds an older match, and `Ctrl+S` or Down
finds a newer one. `Enter` runs the selected command; `Tab` inserts it into the
prompt for editing. `Esc` or `Ctrl+G` restores your original draft and cursor.
Outside the command prompt, `Ctrl+R` keeps its refresh behavior.

Examples: `:po`, `:deploy`, `:sts`, `:svc`, `:nodes`, `:pvc`, `:events`, `:crd`,
`:widgets.example.com`. Discovery uses preferred API versions, including CRDs.
If discovery is incomplete, a warning appears and built-in views remain usable.

`:labels app=web` applies a Kubernetes label selector at the API; `:labels`
clears it. Workload/service Enter follows real selectors; node Enter uses a node
field selector. Escape restores the parent route and selection.

Resource tables follow Kubernetes list/watch streams by default, including
custom resources. If watching is unavailable, tables visibly fall back to polling
at the configured interval (two seconds by default), with failure backoff. The connection
header stays **connected** during healthy background refreshes; **loading** is
reserved for connection/initial loading. Failed requests retain the last good
snapshot with a **STALE** label. Context and route changes cancel requests, clear
old data, and reject obsolete responses. Selection survives refresh and sort.

Click a column header to sort ascending, and click it again to reverse direction.
The underlined header and `↑` / `↓` mark the active sort. `O` cycles through
visible columns, sorting ascending each time; `R` reverses the current direction.
`Shift+Left/Right` or `[` / `]` cycles backward / forward. Mouse and keyboard sorting
preserve the selected resource, and
the order is saved per resource in the active layout. `:sort` opens a searchable
column picker, including custom columns and enabled metrics. Sorting uses numeric
values and exact timestamps for age/event time. Explicit sorting takes precedence
over fuzzy-match ranking. Choosing an order stops live-event following until `G`
resumes it. Mouse clicks select rows or focus the inspector; the wheel scrolls the
pane under the pointer. Drag across readable text to highlight it, then release
to copy. A brief **Copied** confirmation appears at the bottom right. This uses
the terminal's OSC52 clipboard support and preserves original line breaks across
wrapped rows. Selection stays within the pane where the drag started.

YAML and JSON use the active palette's syntax colours in full-screen panels and
inspectors. In the table, `J` opens JSON. With the inspector focused, `J` changes
only the inspector to JSON and `y` changes it back to YAML. In an open manifest
panel, `J` and `y` also switch formats. `:inspector json` / `:inspector yaml` are
available without changing focus. Secret values are hidden by default. Press
`x` on a Secret to reveal decoded values in its inspector/describe/YAML/JSON view;
press `x` again to hide them. Without an inspector, `x` opens the YAML panel.
The view is labelled **values visible · decoded** while revealed. Moving to
another resource, changing scope/context, closing details, or opening the editor
hides values again; reveal state is never saved. Binary values are labelled and
retain base64 text. Custom table columns stay redacted. Highlighting preserves
displayed text, quoting, and spacing.

## Logs

Select a pod, deployment, or statefulset and press `L`. For a workload, kqt reads
all pods matching its selector in its namespace. Multi-container resources offer
a container picker, including an all-containers option. Within logs, `c` selects
a container, `p` toggles previous logs, and `Ctrl+R` reloads.
Logs fill the main pane, keeping the cluster and namespace visible. Container
colour codes and other terminal escape sequences are removed before kqt applies
the selected theme. Highlighting is automatic for each line: complete JSON
objects/arrays get syntax colours, including severity colours for `level`,
`severity`, and `lvl` values. Plain-text logs get subtle timestamp/source colours
and highlighted levels such as `INFO`, `WARN`, and `ERROR`. Mixed formats work
together; incomplete JSON and other text stay readable. Messages keep their
original spacing and order, and saved logs contain plain text without colour codes.
Press `H` (Shift+h) to toggle syntax highlighting without interrupting collection
or moving your scroll position. The title shows `plain` when it is off; search
matches remain highlighted. This also works in the logs-only view. The choice
is saved as `logs.highlight` (enabled by default), and is also available in `O`
log options.

Logs follow live by default. Workloads attach to new matching pods and stop
following removed pods; discovery reconciles every two seconds. Output from
multiple sources identifies each pod/container. A selected individual pod stays
bound to its UID; a replacement requires closing logs and selecting it again.
Streams reconnect using timestamps to avoid replaying already displayed lines.

`s` toggles following the newest lines. With following off, collection continues
and your scroll position stays in place; `s` or `G` returns to the bottom.
`Space` freezes the display while collection continues in a bounded buffer. `/`
opens the filter prompt. Plain text searches for a case-insensitive phrase;
explicit filter terms can be combined:

| Filter | Matches |
| --- | --- |
| `include:timeout !health` | Lines containing `timeout`, excluding `health` |
| `re:"GET /api/[0-9]+"` | A Go regular expression; add `(?i)` for case-insensitive matching |
| `level=warn,error` | Either severity in JSON or recognized plain-text level markers |
| `level>=warn` | Warnings and higher severities |
| `request_id=abc http.status=500` | Exact JSON field values, including nested object fields |
| `message="request completed"` | An exact JSON string containing spaces |

Terms combine with AND; `!` excludes a term (`!re:health` excludes a regex).
`include:` and `exclude:` force literal text. JSON field names and values are
case-sensitive; severity names ignore case and accept aliases such as `warning`.
On non-JSON lines, field queries also match literal `key=value` text.
Positive matches are highlighted in the current theme. Invalid expressions stay
in the prompt without replacing the active filter. Submit an empty prompt to
clear it. Filters survive reloads, container changes, and live/snapshot switches;
opening another resource starts a fresh filter.

`o` saves the displayed, filtered buffer
to a private file and refuses to overwrite existing files. Logs wrap by default;
`w` toggles wrapping, and
`h/l` scrolls unwrapped lines.

`O` opens log options: choose a time window (`1m`, `10m`, `1h`, `24h`, or a custom
positive duration) and an initial line count per container. Both limits apply
together. `all` removes the selected limit; `tail default` restores the original
200-line live / 500-line snapshot defaults. A line count of `0` starts live
collection without initial history. Changing either limit reloads the logs.
`T` toggles Kubernetes timestamps and `N` toggles pod/container source prefixes
without reopening the stream. Application timestamps inside messages stay intact.
Hidden timestamps and prefixes also stay out of copied and saved text; filtering
still searches the original collected lines. These options, wrapping, and the
explicit `s` follow preference are saved under `logs` in the user's config.

`f` toggles a logs-only view that fills the terminal without headers, borders,
or footers. Drag to copy just the log text; incoming lines wait to display until
you release the mouse. `f` or `Esc` returns to the framed log view; another
`Esc` closes logs. Search and save prompts still work in the logs-only view.

`F` switches between live logs and snapshots. Previous logs (`P` / `p`) are
snapshots. By default, snapshots read up to 500 lines / 1 MiB per source, at most
four sources concurrently, and 4 MiB combined. Live collection starts with the
last 200 lines per source. Custom time/line limits change this initial history;
the bounded buffers still apply. Live collection retains at most 4 MiB / 10,000
lines and follows at most 64 containers.
Discarded/truncated output and individual source errors are labelled. Closing
logs, switching contexts, and quitting cancel the streams.

## Events and metrics

`S` (also `E`, or `:status`) shows a readable status summary, pod container states/restarts,
workload replica counts, and conditions with reasons, messages, and transition
times. Colours distinguish readiness from failure/pressure predicates; unknown
custom conditions retain neutral colours. Other custom-resource status fields
appear as highlighted YAML. Resources without status say so explicitly.

The same panel includes **Labels**, **Annotations**, and **Finalizers** tabs.
Press `M` (or `:metadata`) on a resource to open Labels directly; use `1` for
Status, `2` for Labels, `3` for Annotations, and `4` for Finalizers, or cycle with
`Tab` / `Shift+Tab`. The tab bar stays visible while scrolling. Metadata refreshes
with the resource; sorted labels and annotations show compact `key: value` rows.
Multiline annotation values remain indented beneath their keys.
Finalizers appear as a compact list, one highlighted entry per line.
The last-applied-configuration annotation expands into indented, highlighted JSON
when valid; its stored value stays unchanged.
Empty sections say so explicitly. `:metadata annotations` and `:metadata finalizers`
open those tabs directly. `o` saves the active tab to a private file.

Events follow below, matched by immutable UID and updated live. Status refreshes
at the configured interval; events appear oldest first so new activity follows
at the bottom. Scroll with `j/k` or `Ctrl+U/D`; `G` follows the latest event.
`Space` pauses the display while collection continues, `/` filters events, and
`o` saves the displayed status and events to a private file. `Ctrl+R` reloads.
Status remains readable if event permissions/API requests fail, with the failure
shown separately. `R` reverses the table sort.

![Readable status for a restarting pod](docs/screenshots/status.png)
`:events` opens a live event table for the selected namespaces; `0` switches to all
namespaces and `n` selects a namespace. The table supports the same pause,
filter, follow, and export controls. `:events selected` opens the resource status
panel. There is no separate watch command.

Repeat counts update one row per event UID. Each view retains up to 2,000 records
or 4 MiB of event data, evicting the oldest observations. Events removed by the
API stay in this history with a `removed` label. Reconnection resumes from the
last received resource version; expired history is reloaded with a visible gap
notice. Events already expired from Kubernetes retention cannot be recovered.
Event access requires `list` and `watch` permissions; resource status also uses
`get`. Exports use mode `0600` and refuse to overwrite an existing file.

![Live Kubernetes events](docs/screenshots/events.png)

`m` adds CPU and memory usage to pod/node tables. The cluster must provide the
[Metrics API](https://kubernetes.io/docs/tasks/debug/debug-cluster/resource-metrics-pipeline/),
normally through Metrics Server. Missing permissions, unavailable APIs, and
pending samples are reported without disrupting resource browsing. Usage sorts
numerically and is kept separate from the original YAML/JSON manifests.

Custom columns also accept `cpu`, `memory`, `cpu%`, and `memory%`. Percentages
compare pod usage against summed application-container limits, or node usage
against allocatable capacity. If any application container lacks the relevant
limit, the pod percentage is unavailable. Missing samples/limits display `—`.

## Shells and port forwarding

`s` opens `/bin/sh` in the selected running pod, offering a picker when several
containers are running. Configure another command with `shell: "/bin/bash"` in
the settings file. Arguments support quoting and run directly inside the chosen
container. Missing shells and Kubernetes permission denials are displayed.

The shell takes over the native terminal, including resize delivery, then returns
to kqt on exit. `Ctrl+C` goes to the remote process; `Ctrl+]` disconnects locally.
kqt verifies the pod UID immediately before opening the connection. Kubernetes's
exec endpoint addresses a pod by name, so that check is not an atomic UID
precondition. Shells use native client-go; kubectl is not required.

`f` prepares a forwarding command from the pod/service's declared TCP port, or
enter `:forward 8080:80` directly. A local port of `0` chooses a free port. Service
ports resolve named target ports against a ready matching pod; the actual pod and
local address are shown when ready. The selected pod stays fixed for that forward.

`F` / `:forwards` lists active and recently completed forwards. Select one to
inspect its destination and status; `x` stops it. `:forward stop ID` and
`:forward stop all` also work. On a selected pod or service in the resource list
or its YAML/JSON details, `x` stops all forwards associated with that resource.
Service forwards can also be stopped from their resolved target pod. Cancellation
shows `stopping` until the listener closes, then `stopped`; it does not change
unrelated forwards. Listeners bind only to `127.0.0.1`. Up to 16 forwards
can run concurrently; namespace changes retain them, context changes and quitting
close them. Forwarding uses native client-go. Shells and forwards try WebSocket
transport with SPDY fallback for compatible upgrade/proxy failures.

## Ephemeral debug containers

Select a Pod and press `B`, or use `:debug PROFILE`. On first use, choose your own
approved image or explicitly choose public BusyBox, then save it for later use.
Profiles live under `debug.profiles` and support
private ECR references, commands, pull policy, Kubernetes security contexts and
per-context/per-namespace defaults. Defaults run non-root, drop all capabilities,
disable privilege escalation and use RuntimeDefault seccomp.

The flow checks scoped permissions and server-side admission automatically,
then shows the selected Pod, image and target. Press `Enter` to start; `c` changes
the container and `d` shows full details. It joins the
existing Pod, preserving node placement, tolerations and pull credentials, and
uses `pods/attach` without requiring `pods/exec`. Read-only mode disables it.
`Ctrl+]` disconnects; `:debug attach [NAME]` reconnects. Use `exit` to stop the
shell; its ephemeral entry remains in the Pod spec.

See [debug configuration, RBAC and lifecycle details](docs/debug-containers.md)
for a private-registry example and cluster compatibility notes.

## Editing

`e` fetches a fresh, full resource and opens a private temporary YAML file in:

1. The config's `editor` command, if set.
2. `$EDITOR`, then `$VISUAL`, then `vi`.

Commands may contain quoted arguments, for example `code --wait` or `nvim -f`.
They run directly, without a shell. Use an editor's wait/foreground option so it
exits only after you finish editing.

Saving changes and exiting successfully opens a highlighted diff. Press **Enter**
to apply the reviewed changes or **Escape** to discard them. Secret values remain
hidden in the diff; changed keys are marked. Applying sends a minimal JSON merge patch with
UID and resourceVersion preconditions. Exiting without changes performs no
write. Identity fields cannot change; status and managedFields are omitted and
cannot be edited. Kubernetes enforces schema validation and update permissions.
Conflicts, invalid YAML, or editor failures keep the private file and display its
path for recovery. Successful/unchanged edits remove the temporary file and
refresh the table. In demo mode, edits affect only the sample backend.

Secrets are hidden by default in browsing views, with `x` for temporary decoded
inspection. Editing a Secret fetches its original API data, so decoded values
or redaction placeholders cannot overwrite it. The edit file's directory and
file permissions are private. Pod logs and other resources may contain sensitive
content; ordinary Kubernetes RBAC still applies.

## Saved settings and layouts

Settings live in `$XDG_CONFIG_HOME/kqt/config.yaml`, or
`~/.config/kqt/config.yaml`. Missing files use defaults and are created when
preferences are saved. View and namespace selection are saved automatically, per context.

Use `:ns prod-dep,prod-ccp,prod-global,prod-system` to combine specific namespaces
in one table, including live resources, events and pod metrics. The namespace
column appears automatically for a multi-namespace selection. Tab completes each
name in the comma-separated list. Select up to 64 namespaces; spaces and repeated
names are normalised.

Run `:ns @save production` to name the current selection, then `:ns @production`
to return to it. `:ns save production` remains a compatibility alias. Groups are
saved under the actual kubeconfig context in the user's config file, so the same
group name can select different namespaces in different clusters. The header
shows the group name and namespace count.

The `@` prefix distinguishes groups from literal namespaces: `:ns groups` selects
a namespace named `groups`, while `:ns @groups` selects the saved group `groups`.
Even group names such as `save` and `edit` work: `:ns @save` or `:ns @edit`
selects that group; a space and arguments introduce a management command.
Tab after `:ns @` cycles through actions and saved groups; Shift+Tab cycles back.
For example, `:ns @ed<Tab>` fills `:ns @edit `, then `dev<Tab>` fills the saved
group name `development`. With `:ns @edit <Tab>`, no prefix is needed: Tab cycles
through all saved groups in the current context, and Shift+Tab cycles back.
After completing a unique action, pressing Tab again starts its group cycle.
New names for saving or renaming are typed freely. Enter executes the command.

Open `:ns @` for a fuzzy-searchable group manager showing names, membership and
the active selection. Type to search, use arrows or Ctrl+J/K to move, and Enter
to select. Its management shortcuts are:

| Key | Action |
|---|---|
| `Ctrl+N` | Save the current namespace selection under a name |
| `Ctrl+E` | Edit the selected group's prefilled comma-separated namespace list |
| `Ctrl+R` | Rename the selected group |
| `Ctrl+D` | Delete the selected group after a compact Yes/No confirmation |
| `Esc` | Close the manager or cancel an edit |

The same actions are available as commands:

```text
:ns @save development
:ns @edit development
:ns @rename development dev-team
:ns @delete dev-team
```

Enter saves an edit; Escape cancels it. Saving under an existing name replaces
its membership; renaming refuses an existing destination. Groups require explicit
namespace names, so select specific namespaces before saving (not `:ns -A`).
Editing or saving a definition does not change the current filter: use
`:ns @NAME` to apply it. Renaming or deleting also leaves the selection intact.
Deleting a group only removes the saved definition, never Kubernetes namespaces
or resources. Deletion defaults to **No**.

Each namespace is queried and watched separately, so the combined view works
with namespace-scoped RBAC. If a namespace is unavailable, usable results remain
visible with a partial-results warning; previously loaded rows from that namespace
are retained until a successful refresh. `Ctrl+R` retries. Cluster-scoped resources
such as nodes are listed once. `:ns -A` or `0` selects all namespaces.

Theme selection, header visibility, inspector format, and chosen layout are also saved. Launch flags
override saved values. A theme preview is not saved until Enter; Escape cancels it.
The theme name appears in the header for ten seconds after selection.

```sh
kqt config                      # Print a YAML example
kqt config --validate
kqt config --validate --config ./my-config.yaml
```

```yaml
version: 1
theme: catppuccin-mocha
layout: troubleshooting
view: pods
namespace: default
inspector_format: yaml
refresh_interval: 2s
history_limit: 100
editor: "code --wait"
download_dir: "~/Downloads/kqt"
logs:
  since: 10m          # omit for all available history within the line limit
  tail_lines: 1000    # omit for live 200 / snapshot 500; -1 all; 0 no initial lines
  timestamps: true
  highlight: true
  prefixes: auto     # auto, "on", "off"
  wrap: true
  follow: true
context_aliases:
  prod: "actual-production-context"
contexts:
  actual-production-context:
    namespace: prod-dep,prod-ccp,prod-global,prod-system
    namespace_groups:
      production: [prod-dep, prod-ccp, prod-global, prod-system]

views:
  pods:
    columns: [namespace, name, ready, status, restarts, age, node]
    sort: name

layouts:
  troubleshooting:
    extends: inspector
    inspector_percent: 40
    wide: false
    views:
      pods:
        columns: [namespace, name, ready, status, restarts, node, age]
        sort: restarts
        descending: true
```

Built-in layouts are `classic` (no inspector), `inspector` (right pane at 120+
columns), and `bottom` (bottom pane at 24+ rows). Named layouts can inherit one
another, choose `none` / `right` / `bottom`, and override the inspector percentage,
columns, sorting, and wide mode. `:layouts` searches all built-in and saved names.
`:write NAME` saves the current configuration into the same YAML file; aliases
`:layout save NAME` and `:layouts NAME` also work. Smaller terminals collapse a
right inspector; full-screen details stay available.

`:columns name,status,age` saves columns for the active layout. `:sort age` and
`O` / `R` change sorting; `:write NAME` includes that sort in the saved profile.
JSON Pointer columns such as `/spec/replicas` and
`/metadata/labels/app.kubernetes.io~1name` are supported. Numeric fields sort
numerically; age uses creation time.

Wide mode (`w`) lets long columns use spare table space before truncating;
smaller terminals keep horizontal scrolling. Labels use `key=value, key=value`
spacing, with distinct theme colours for keys, values, equals signs and commas.
The selected row keeps readable contrast, and `no-color` uses text emphasis.

kqt bundles 37 themes, including `material`, `palenight`, `dracula`, `darcula`,
`monokai`, `ubuntu`, `lemonade`, and `pink-lemonade`. The VS Code references are
Radu Matei's Ubuntu Color Theme, Denner's Lemonade, and Casseopeian's light pink
Pink Lemonade. Ayu includes `ayu-dark`, `ayu-mirage`, and `ayu-light`.
Everforest includes `everforest-dark-hard`, `everforest-dark-medium`, `everforest-dark-soft`,
and the matching `everforest-light-*` variants. Use `:themes` to preview them or
set one directly, for example `:theme ayu-mirage` or
`:theme everforest-dark-medium`. `kqt themes` lists them. `--theme`
overrides `KQT_THEME`, which overrides the saved theme.

`accessible-dark` and `accessible-light` use blue, amber/yellow, and reddish
purple status colours with contrasting selections and readable input fields.
Choose either with `:themes` or `:theme accessible-dark`. Across themes, focused
panes have a `›` heading marker, log search matches are underlined, and known
Kubernetes conditions include `OK`, `Attention`, or `Problem` interpretations
alongside their original values. See [accessibility design and previews](internal/tui/THEMES.md#accessible-presets)
for the contrast targets, sources, and validation limits.

Select `no-color` from `:themes`, run `:theme no-color`, or launch with
`kqt --theme no-color` to use your terminal's default foreground and background
without colour styling. Bold, underlining, and reverse-video selections keep
the interface readable. The choice is saved like other themes; `NO_COLOR`
continues to suppress colours regardless of the selected theme.

Define custom themes in TOML files under `themes/NAME.toml` beside the settings
file:

```toml
version = 1
name = "company"
extends = "nord"
[colors]
accent = "#8be9fd"
```

See [palette attribution and upstream notices](internal/tui/THEMES.md) for theme
licenses.
Config errors and inheritance cycles fail visibly. Preference writes preserve
unrelated YAML settings and comments and replace the file atomically. An invalid
file is not overwritten. Restart to load manual configuration changes.

If no YAML configuration exists, kqt loads the default `config.toml` and writes
its settings to `config.yaml` when preferences are saved, preserving the TOML
file. Explicit TOML config paths are supported. No connection credentials are
stored in kqt's UI config.

## Development and verification

```sh
go test -race ./...
go vet ./...
python3 scripts/tui-smoke.py --binary bin/kqt
```

`just check`, `just smoke`, and `just smoke-live` are provided. The live check uses
Docker, k3d, and kubectl to create a disposable cluster, apply controlled
fixtures, exercise the TUI, and clean up, with a separate temporary kubeconfig.
Optional `--snapshots artifacts/demo` records screen text and ANSI.

Tests cover API contracts, selectors, pagination, discovery, cancellation,
versioned editing, secret redaction, saved settings and migration, layout
inheritance, completion, fuzzy matching, syntax highlighting, theme timing,
inspector focus, terminal dimensions, and log aggregation. PTY checks cover
editor terminal handoff/restoration and saved-state restart as well.

See [the design](docs/design.md), [verification](docs/verification.md), and
[the pod-shell assessment](docs/pod-shell-assessment.md).

## Related resources

Press `b` or run `:related` (`:relations`) for a searchable list of related
resources. Enter follows a link; Escape returns to the previous resource view.
Owner chains follow immutable owner UIDs. Workloads/services/nodes expose matching
pods; Deployments expose owned ReplicaSets and CronJobs expose owned Jobs.
Ingresses link to backend Services, pods link to their node and mounted PVCs,
and PVCs/PVs link to their bound storage counterpart. Permission failures or
missing/replaced references leave available links usable and show a warning.

## Copying and exporting manifests

Set `download_dir: "~/Downloads/kqt"` in `~/.config/kqt/config.yaml` to choose a
default directory for all file exports: manifests, logs, events, status, and
metadata. You can also set it with `:download-dir ~/Downloads/kqt` (quote paths
containing spaces), inspect it with `:download-dir`, or reset it with
`:download-dir clear`. The choice is saved across restarts.

Save prompts suggest a path in that directory, and relative paths passed to
`:export` or entered in a save prompt use it too. Absolute paths and `~/...`
override the default. Relative directory settings are resolved from the current
working directory. Without a setting, exports use the current directory.
Missing parent directories are created on save with mode `0700`; files use
`0600`, and existing files are never overwritten.

`C` copies the selected manifest using the terminal's OSC52 clipboard support;
`o` prompts for a private file destination. Both work in YAML/JSON details.
`:copy yaml`, `:copy json clean`, and `:export json clean "saved manifest.json"`
choose the format explicitly. The `clean` option removes generated metadata,
owner references and status while preserving resource specifications. Exported
files use mode `0600` and never replace an existing file. Clipboard support varies
by terminal; the notice confirms that the request was sent, and file export is
available independently. Clipboard requests are limited to 1 MiB.

Secret exports are redacted while values are hidden. After `x` reveals a Secret,
copy/export uses its original Kubernetes base64 data, rather than the decoded
inspection form. Hiding values restores redacted exports.

## Workload actions

`a` / `:actions` lists actions for the selected resource. Deployments,
StatefulSets and ReplicaSets support `:scale N`; Deployments, StatefulSets and
DaemonSets support `:restart`. CronJobs support `:suspend`, `:resume`, and `:run`
(create one Job immediately from the job template). `D` / `:delete` requests
normal Kubernetes deletion of the selected resource.

Deletion shows a compact, centred **No / Yes** dialog with the context and resource.
**No** is selected initially. Choose with arrows or Tab and press Enter, click a
button, or press `y` / `n`. Escape cancels. Other workload actions require the exact
`namespace/name` (or name for a cluster-scoped resource) to confirm.
The resource UID and desired specification are checked before applying; patches
and deletions carry identity/version preconditions. Background status updates
are accepted, while changed specifications require a fresh selection. Permissions are enforced by Kubernetes. A successful request
is reported separately from controller reconciliation; `S` shows rollout status
and events. Cancelling a request already in progress does not roll back changes;
refresh to check its final state.

## Resource diagnostics and bundles

On a **Service** or **Pod**, press `S` then `5`, or run `:diagnose`. `Tab` and
`Shift+Tab` cycle the status panel's five tabs. Diagnostics refresh while open;
`Ctrl+R` reloads immediately. Other resource kinds keep the four existing tabs.

Service diagnostics show selectors, matching Pods, published EndpointSlice
addresses and readiness, and Service-to-container port mappings. They flag empty
selectors/backends, missing named ports, port mismatches and stale target UIDs.
Selectorless Services and ExternalName Services are explained separately. This
view reads API objects; it does not probe DNS, NetworkPolicies or connectivity.

Pod scheduling diagnostics show the PodScheduled condition, retained scheduler
events, tolerations, node selectors, affinity/topology constraints, per-container
requests, node taints/readiness/allocatable resources, and PVC dependencies.
Node observations do not replace the scheduler's decision: available capacity
after other Pods, affinity matching, volume topology and custom scheduler plugins
are not fully evaluated. Missing permissions appear as unavailable data rather
than a healthy result. Node access is optional.

`:bundle` prompts for a `.tar.gz` destination in the configured download directory.
You can also provide a path: `:bundle "incident/api.tar.gz"`. The archive contains
`resource.yaml`, `status.yaml`, available events, related resource summaries,
Pod/Service diagnostics when applicable, and capture notes in `README.txt`.
`:bundle --logs` explicitly adds log snapshots (at most 20 Pods, 200 lines per
container and 4 MiB total). Both commands work in read-only mode.

Secret resources are excluded, including when their inspector values are
revealed. Literal environment values, ConfigMap data and last-applied annotations
are redacted or omitted. Other annotations, command arguments, events, status and
optional logs may contain sensitive application data; review the archive before
sharing. Missing RBAC/API data is recorded in the archive's notes. Files and
archive entries use private permissions, existing destinations are never
overwritten, and Escape cancels capture. Captures reject a replaced selected
resource and represent best-effort observations, not an atomic cluster snapshot.

## Deployment rollout history

Select a Deployment and press `H`, or run `:rollouts` (`:rollout history`). The
picker lists retained revisions, images and change causes, newest first. Enter
opens the pod template and the ReplicaSet's creation time. `d` compares the
selected template with the Deployment as it was when history loaded; `y` returns
to highlighted YAML. `Ctrl+R` reloads, and Escape returns to the revision picker.

Press `r` in revision details, or use `:rollback 1`, to prepare rollback. The
existing confirmation dialog requires the exact namespace/resource name before
applying. Rollback restores the selected pod template while preserving the
current replica count and rollout strategy. It checks Deployment/revision
identities and template changes again, and uses the latest resource version for
the patch. Paused Deployments must be resumed before rollback. Read-only mode
allows history and comparison while disabling rollback.

History comes from ReplicaSets owned by the selected Deployment. Revisions
removed by Kubernetes' [revision history limit](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/#clean-up-policy) are unavailable. This feature
currently supports Deployments.

## Live resource tables

Ordinary resource tables use Kubernetes list/watch streams. Added, modified
and deleted objects update the table and inspector while preserving selection.
Expired watch history triggers a full resync; partial lists never replace the
last complete snapshot, and resources absent from the new list are removed.
Reconnection retains visible data and labels stale states. `Ctrl+R` / `:refresh`
restarts the list/watch. Context and scope changes, editor/shell handoff and quit
cancel the watch. Metrics continue refreshing independently at the configured
interval. Watch permission/API failures leave browsing available through a
labelled polling fallback. Revealed Secret values are hidden when that Secret
changes; press `x` to fetch and reveal its current values again.

## Permissions and read-only mode

`A` / `:permissions` (`:access`) shows your API-reported identity and current
resource permissions, including pod logs, shell and forwarding subresources.
`:can-i get`, `:can-i patch`, or `:can-i get log` checks one action. Results are
labelled Allowed, Denied or Unknown; a failed/unavailable review is never treated
as a denial. Kubernetes admission policies can impose further rules. `Ctrl+R`
rechecks and Escape closes the panel.

Start with `kqt --read-only` or configure `read_only: true` to disable editing,
workload operations, pod shells and debug containers. The header displays **READ ONLY**. Inspection,
logs/events, permission reviews, clipboard/file exports and port forwarding remain
available. `--read-only=false` explicitly overrides a configured read-only default
for that session. This mode restricts kqt's controls; it does not alter your
Kubernetes credentials or replace cluster authorization.

## Cluster overview

`U` / `:overview` opens cluster health, workload counts, node pressure and
allocatable capacity, observed CPU/memory usage, unhealthy resources and the ten
most recent warning events. Workload/event scope follows the current namespace;
node capacity and usage are labelled as cluster-wide. Missing permissions or
metrics are shown as unavailable, with sample coverage reported for usage totals.

The summary refreshes automatically every five seconds or the configured interval
if longer. `Ctrl+R` reloads, `0` switches to all namespaces, `n` opens namespace
selection, and Enter opens a searchable list of unhealthy resources to explore.
Use arrows or the mouse wheel to scroll; Escape returns to the resource table.
