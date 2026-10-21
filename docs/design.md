# kqt: A Kubernetes Query Tool

kqt carries Maestro's visual language into a Kubernetes explorer: compact
context header, framed tables, contextual footer, command prompt, fuzzy pickers,
independently scrollable inspector, and the same semantic palettes.

## Foundation

Use Maestro's pinned Charm versions: Bubble Tea v2.0.10, Bubbles v2.2.1,
Lip Gloss v2.0.6, and x/ansi v0.11.8. Palette values and notices came from
`maestro/internal/tui`. Official client-go v0.36.0 supplies kubeconfig loading,
authentication, discovery, dynamic resource reads/patches, and the typed core
log client. There is no runtime dependency on Maestro or kubectl.

References: [k9s](https://k9scli.io/),
[client-go](https://github.com/kubernetes/client-go),
[Bubble Tea](https://github.com/charmbracelet/bubbletea).

## Sessions and presentation

- Contexts come from explicit flags or kubeconfig current-context; saved UI
  state never silently changes the Kubernetes target. Context switches are
  explicit, cancel requests, clear data, and restore that context's view/scope.
- Async results carry session/request tokens. Obsolete list, log, and editor
  results are discarded. Only one list request runs for a route at a time.
- Resource tables use paginated list/watch streams, with labelled polling
  fallback when watch access is unavailable. The connection
  label reflects health, not every polling request's transient busy flag.
  Failed refreshes keep the last good snapshot and visibly mark it stale.
- Discovery includes listable preferred resources and CRDs, with built-in
  definitions retained after partial failures. Ambiguous names require a
  qualified API resource name.
- Namespace, workload selectors, and node field selectors are sent to the API.
  Selection is preserved by UID; relationship history restores scope and row.
- Fuzzy search matches each field independently and ranks subsequence matches,
  avoiding matches assembled from unrelated pod/restart/node digits. Printable
  navigation keys remain text in inputs. Tab completes only; Enter executes.
- YAML tokenization maps source tokens to existing theme colours without
  reserializing. If source text cannot be preserved, display plain text.
  JSON matches entire strings before numbers/punctuation. Secret redaction and
  control sanitation happen before highlighting. Both formats work in the
  inspector and panels; theme previews preserve inspector scrolling.
- The selected theme is announced for ten seconds, with versioned timer
  messages so an earlier timer cannot hide a later selection.

## Preferences and layouts

`~/.config/kqt/config.yaml` (or XDG/explicit path) stores current view/namespace,
per-context state, theme, inspector format, editor, columns, and named layouts.
Explicit launch flags take precedence. Default TOML settings are migrated on
first save while leaving their source unchanged; custom palettes remain TOML.

Inspector visibility and placement have separate controls: `i` toggles visibility
and `I` opens the right/bottom chooser. Optional `inspector_visible` and
`inspector_position` preferences override the active profile without reapplying
its columns, sorting, width percentage or wide state. Omitting visibility uses
the profile's default, preserving existing configs. Selecting a layout clears
the visibility override and applies its arrangement; saving a layout captures
the effective inspector state. Hidden inspectors retain their preferred position.
Rendering temporarily uses the other position when only that position fits,
without saving the fallback. Hiding clears inspector focus and revealed Secrets.

Built-in and inherited custom profiles choose inspector arrangement/percentage,
per-resource columns/sort, and wide mode. `:layouts` selects a profile; `:write`
saves the current configuration. Cycles, invalid names, and invalid fields fail
validation. Changes patch the latest YAML tree, retaining unrelated values and
comments, then use a private temporary file and atomic rename. Failed writes
leave the current UI usable and display the error.

## Logs

Pod logs support live following and bounded snapshots. Deployment/statefulset
logs list actual matching pods in the object's namespace. Snapshots read the
selected container or all containers with four workers and a 4 MiB total bound.
Live logs reconcile membership every two seconds, retain timestamp checkpoints
across reconnects, and cap concurrent streams at 64. A serialized callback feeds
a bounded queue; the UI retains at most 4 MiB / 10,000 lines. Pausing freezes
rendering while collection continues. Search is a case-insensitive text filter;
exports write the displayed buffer to a new private file. Individual source
failures remain visible alongside healthy output. Single-pod streams stop on
UID replacement; context changes/closing the pane cancel all sources.
The UI removes complete ANSI escape sequences before buffering or displaying
logs. Logs replace the main table/inspector pane, keeping the connection header
visible; search and exports use the same cleaned text.

Log highlighting detects complete JSON objects/arrays independently for each
line, after optional source, RFC3339 timestamp, and severity prefixes. It colours
the original JSON tokens without reformatting or converting numeric values;
`level`, `severity`, and `lvl` string values also use severity colours. Ordinary
messages highlight explicit log-level fields/brackets and uppercase levels.
Malformed/partial JSON remains plain text, so multiline fragments and mixed
sources need no parser mode. The current theme supplies all colours. Rendered
lines are cached only while retained in the log buffer; eviction, stream closure,
and theme changes release or invalidate those entries. Filtering/export use
plain text, so searches can span coloured tokens and exports contain no ANSI.

## Column ordering and mouse input

Bubble Tea v2 supplies native mouse click, release, and wheel messages through
`View.MouseModeCellMotion`; no additional dependency is needed. Mouse reporting
is disabled while editing or running a native shell. Table rendering and hit
testing share column widths, horizontal offsets, pane dimensions, and the visible
row range. This keeps headers/rows aligned in classic, right-inspector, and
bottom-inspector layouts, including Unicode and clipped custom columns.

Clicking a header sorts ascending or reverses the current column. `O` cycles
forward through visible columns, wrapping to the first and sorting ascending.
Shift+Left/Right (or `[` / `]`) cycles backward/forward; `R` reverses the current
order. The active header is underlined. `:sort` offers a searchable column picker and
`:sort COLUMN asc|desc` specifies an exact direction. Sort choices are saved per
resource in the active layout. Explicit ordering takes precedence over fuzzy
match ranking, retains selection by UID, and uses exact age/event timestamps and
numeric metrics/counts. Manual event ordering stops following until `G` resumes.
Input fields and modal panels prevent header clicks from changing the table.
Row clicks select without opening resources, inspector clicks focus it, and wheel
messages scroll the pane under the pointer.

The column controls build on [K9s's table bindings](https://github.com/derailed/k9s/blob/master/internal/view/table.go), with `O` cycling and applying the next column directly.
Mouse support comes from [Bubble Tea's view API](https://github.com/charmbracelet/bubbletea/blob/v2.0.10/tea.go).

## Events, metrics, and connections

`:events` starts a client-go list/watch for the current namespace, with all
namespaces available through the ordinary scope controls. Selected-resource
events use a UID field selector and fresh identity checks. `S` / `:status` renders
a themed status summary, workload
replica counts, pod container state/restarts/last exits, and condition reasons,
messages, and transition timestamps before events. Standard negative predicates
(for example node pressure or failure) use the opposite colour polarity to
readiness; unknown custom predicates stay neutral. Stale condition generations
are labelled. Custom status fields remain available as highlighted YAML, and
resources without status say so. Event-read errors do not hide readable status.
Status refreshes at the configured interval while events follow a watch stream,
oldest first. Repeat counts update by UID. `Space` freezes the displayed view
while collecting, `/` filters, `G` follows the latest event, and `o` exports the
displayed content using exclusive creation with mode 0600. `Ctrl+R` reloads;
scrolling stays local to the panel. Status replaces the main
pane, preserving the connection header without background inspector fragments.
`S` opens status (with `E` retained as an alias); `R` reverses sort direction.

The initial event list is paginated and establishes a resource-version
checkpoint. Watches accept bookmarks, renew after server timeouts, reconnect
with backoff from the last checkpoint, and relist on HTTP or stream 410 errors.
Relists leave a visible history-gap marker. Both the producer queue and retained
view cap event data at 4 MiB / 2,000 records, coalescing by UID and evicting the
oldest event occurrences. API deletion marks retained history as removed.
Rendering is coalesced to at most ten updates per second. List/setup requests
have 15-second bounds; established watches renew within 75 seconds, independent
of the ordinary client request timeout. Context/scope/view changes cancel the
source and reject old replies. A replaced/deleted selected resource ends its
watch; temporary status-read failures keep previous status with a stale label.
The watch lifecycle follows the [Kubernetes API watch semantics](https://kubernetes.io/docs/reference/using-api/api-concepts/#efficient-detection-of-changes).
Node condition meanings follow the [Kubernetes Node Status reference](https://kubernetes.io/docs/reference/node/node-status/).
Deployment progress follows the [Deployment status documentation](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/#deployment-status).
Metrics try the stable API
then the older beta API when absent. Metrics requests have a three-second bound;
failure never turns a successful resource read into a failed read. Usage remains
separate from unstructured resources, preserving YAML/JSON exactly. Sorting uses
numeric quantities; missing samples/limits remain unavailable.

Shells use client-go remotecommand through Bubble Tea Exec, handing the native
terminal over without an embedded emulator or kubectl dependency. A cancelable
input reader and bounded queue consume Ctrl+] even during connection setup.
Polling terminal size supplies the remote size queue, and all terminal input
workers end before returning to Bubble Tea. The selected running container is
revalidated against the pod UID immediately before exec.

Port forwards use client-go, expose only 127.0.0.1, and bind to one reported pod.
Service ports resolve numeric/named target ports against ready matching pods.
Up to 16 connections run concurrently, with bounded recent history. Context
changes and quitting cancel pending and active forwards. Both connection types
retain session TLS/authentication, prefer WebSocket, and use SPDY fallback for
compatible upgrade/proxy failures. A small upgrade wrapper closes the TCP socket
on context cancellation during a stalled handshake; the SDK owns established
streams. Handshakes are bounded without imposing that timeout on the session.

## Secret inspection

List rows and custom columns remain redacted. `x` on the selected Secret fetches
a fresh object with the existing Kubernetes client, checks its UID, and reveals
a display-only copy with decoded UTF-8 data in describe/YAML/JSON/inspector views.
Binary data is labelled with its byte count and retained base64 representation;
`stringData` is already text. Managed fields and the duplicate last-applied
annotation remain omitted. Titles and the status line identify decoded visible
values. Refreshes update only the revealed object; other objects stay redacted.

A second `x` cancels a pending read or hides values. Selection, scope/context
changes, closing details, quitting, and opening the editor discard the revealed
object and redraw redacted content. Reads have a 15-second timeout; generation
and request tokens reject late replies. Reveal state/data is never saved to
configuration. When no inspector is displayed, `x` opens the YAML panel.

## Editor lifecycle

Editing is an explicit `e` / `:edit` action. Fetch fresh data and validate the
selected UID before preparing a private file. Original Secret data is used for
editing; temporary decoded browsing values are never used for a patch. Pause
polling and use Bubble Tea's
`ExecProcess` to release/reclaim the terminal around a directly executed editor
command, parsed with quote/escape support and no shell expansion.

On successful editor exit, strictly parse one YAML document, preserving numeric
precision. Resource identity and version cannot change; status and managedFields
are excluded. Compare with the editable source and perform no write if unchanged.
Otherwise freeze a minimal merge patch and show a highlighted diff before any
write. Enter applies that exact reviewed patch; Escape discards it and deletes
the private file. Secret values remain redacted, with changed keys marked.
Send the reviewed merge patch with original UID/resourceVersion
preconditions. Kubernetes supplies schema validation and authorization. Keep the
file on failure/conflict; delete it on success or an unchanged edit. Resume the
resource table and its watch after return.

Editing and confirmed workload actions can mutate resources; explicit shells
can change container state. Read-only mode blocks these paths locally. Resource
relationships, generic watches, permission reviews, manifest exports and the
cluster overview are implemented. Qualification uses backend contract tests,
model tests, and PTY checks against demo and a disposable real Kubernetes cluster.

## Read-only diagnostics and bundles

The fifth resource-panel tab is available only for Pods and Services. Reports
share structured sections between themed terminal rendering and plain-text bundle
exports. Fresh GETs verify the selected UID; optional reads produce explicit
unavailable sections. Reports use independent cancellation tokens under the
status session, refresh at most once every five seconds, and cannot resurrect a
closed view. Node observations are deliberately narrower than a scheduler
simulation. Service readiness counts refer to deduplicated published addresses,
with terminating/serving conditions shown separately.

`:bundle [--logs] [PATH]` collects bounded snapshots under a cancellable deadline.
A filtering backend prevents Secret GET/LIST calls while collecting relationships;
only relationship identities/reasons are exported. Known secret-bearing manifest
fields are redacted before serialization. Optional logs remain explicitly opt-in
and are not claimed to be redacted. The selected UID is checked again at the end.
The archive has fixed, flat entry names and private entry modes; saving uses the
existing exclusive-create download path logic and removes partial writes.

Deletion uses a compact centred No/Yes dialog with No selected initially.
Keyboard, rendering and mouse hit testing share the same modal bounds. Existing
UID/specification and deletion version preconditions still gate the operation.

## Namespace selections and saved groups

Namespace scope accepts an explicit, normalized comma-separated list of up to 64
names. Empty members and mixed wildcard selections fail validation. Namespaced
list and metrics requests use individual namespace endpoints with at most four
requests in flight; no cluster-wide list permission is required. Cluster-scoped
resources are requested once. Watches retain independent resource versions and
relist boundaries per namespace, with serialized callbacks into the model.
An unavailable namespace does not stop healthy streams. Partial-results warnings
remain visible, and previously loaded rows from failed namespaces are retained
until refreshed successfully. Node-to-pod navigation preserves the selected scope.

`:ns save NAME` stores an explicit selection under the actual kubeconfig context;
`:ns @NAME` selects it. Groups and the current namespace selection survive normal
route/preference saves. Multi-namespace tables always identify each row's
namespace, including when a custom column configuration omits that column.

## Cached command history

Successful commands use a separate private JSON cache, independent of ordinary
config writes. `history_limit` is a positive integer, defaults to 100, and applies
to in-memory history, persisted entries, restart, recall and reverse search.
Entries retain submission order when asynchronous operations complete out of
order. Lower limits retain the newest commands.

`XDG_CACHE_HOME` takes precedence. The macOS fallback is `~/.local/cache/kqt/`;
Linux and FreeBSD use the system cache directory. Writes use a private temporary
file, sync and atomic replacement. Legacy YAML/TOML history is copied before its
config field is removed; cleanup retries avoid duplicate imports. Cache errors
are warnings, with command execution and in-memory recall still available.
History is loaded/migrated only when starting the TUI, so config validation and
theme listing remain independent of cache availability.
