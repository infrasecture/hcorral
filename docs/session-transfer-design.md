# Session transfer implementation decisions

Design reference: myCodex `.proposals/hcorral-go-successor.md`, phase 5, and
`.proposals/codex-session-transfer.md`. This is an implementation record. Public
commands are now wired on the development branch; unresolved consistency and
runtime qualification gates below still prevent claiming release readiness.

## Reproducing storage mounts

The endpoint retains bind propagation from Docker's actual `Mounts` inspection
and recursion options from `HostConfig.Mounts`. The helper preserves those
options instead of applying `rprivate` to every bind. Volume subpaths continue
to come from the deployed volume definition. Access is never upgraded from a
deployed read-only mount. The helper deliberately omits source-directory creation:
a vanished bind source must fail instead of becoming empty replacement state.

The transport currently supports only default/consistent bind modes.
Cached/delegated modes, SELinux relabel modes, unknown propagation or
mode values, and contradictory recursive options are refused. Transfer does not
relabel existing host storage. Unrelated workspace/GUI mounts remain outside
the selected storage and are not rejected for their own unused bind options.

A separate SQLite bind is narrowed to its selected directory when safe. A
nonrecursive parent bind cannot be narrowed this way: resolving its daemon-side
subdirectory could enter a submount that the original workstation excludes.
Such a layout requires a direct mount of the database directory. No fallback
widens the helper to the whole workspace. Docker clients lacking a requested
mount option fail creation explicitly; the transport never removes that option
and retries with weaker semantics.

This mapping follows the [Docker bind-mount contract](https://docs.docker.com/engine/storage/bind-mounts/),
[Engine bind options](https://github.com/moby/moby/blob/master/api/types/mount/mount.go)
and [CLI mount parser](https://github.com/docker/cli/blob/master/opts/mount.go).
The unit checks verify inspection, metadata narrowing, refusal and reinspection
after helper copy. Real Docker bind/subpath cases are part of the integration
suite; their presence alone is not runtime qualification.

## Source format

The first implementation targets the native format researched at Codex
`rust-v0.160.0`, tree `a956835d020762cb2b570053af06f643a11c0ecc`.
Source contracts checked:

- `rollout/src/rollout_file_name.rs`: ordinary thread/rollout IDs and the
  separate `_rollout-id` suffix after revert.
- `thread-store/src/local/thread_rollout_resolver.rs`: SQLite selects the
  current paginated rollout; a stale authoritative row must not select a
  different old rollout through a filename scan.
- `thread-store/src/local/rollout_lineage.rs` and `protocol/src/protocol.rs`:
  `history_base.thread_id` references a rollout ID, with exclusive ordinal
  and decoded-byte boundaries. A child's metadata ordinal starts at its
  inherited boundary. Ordinals need not be contiguous.
- `rollout/src/writer_lock.rs`: cross-process writer coordination.
- `state/src/sqlite.rs`: the selected-thread metadata lives in `state_5.sqlite`.

The Go implementation reads these contracts; it does not copy Rust source or
reserialize its event types. Unknown event payloads and metadata fields remain
in the original JSONL bytes. Legacy copied ancestor metadata does not change
the identity of the containing conversation.

The pinned source is available in the [Codex repository](https://github.com/openai/codex/tree/a956835d020762cb2b570053af06f643a11c0ecc/codex-rs).

## Implemented inspection core

`internal/session` contains path resolution, filename indexing, SQLite
selection, streaming rollout validation, dependency planning and writer guards.
It has no Docker, GUI or workstation lifecycle side effects.

Host path precedence is explicit path, nonempty host `CODEX_HOME`, then
`$HOME/.codex`. Relative paths belong to the caller's directory. Opening a
home resolves its root symlink once. Rollout descendants are opened component
by component with directory descriptors and `O_NOFOLLOW`; FIFOs and other
special files cannot block opening and are rejected. Enumeration reads names,
not the contents of unrelated conversations.

Source SQLite queries use `mode=ro`, a read transaction, query-only mode and a
bounded busy timeout. They deliberately do not use `immutable=1`, which could omit
committed WAL records. Only the requested thread's selected path, history mode
and archive status are queried. Source databases and sidecars are never part
of a transfer plan. The native SQLite VFS opens its own descriptors: checked
database/sidecar paths and database identity validation supplement the rooted
rollout I/O, but this is not an atomic filesystem snapshot against arbitrary
same-user file replacement. Qualify that boundary before destination writes.

The inspection API requires the effective SQLite home explicitly, allowing a
separate state directory. Endpoint code now supplies the file/env resolution or
an explicit user selection described below; it does not infer that state always
lives under `CODEX_HOME`.

Ambiguous rollout IDs without authoritative selection fail. Plain/compressed
representations can be deduplicated only when their decoded selected bytes
match. Unknown database generations fail; older unused databases alongside a
supported current database do not override it. A missing, inconsistent or
out-of-root authoritative selection fails without a speculative fallback.

Plans contain SHA-256 hashes, decoded sizes, modification times, native
metadata and required files in prerequisite-first order. Compressed logs are
decoded to byte-identical JSONL. The parser does not have Scanner's small
default line limit. Explicit resource limits currently default to 256 MiB per
record, 64 GiB per decoded file and 4,096 lineage files. Public transfer flags
allow these limits, and the 8 MiB manifest limit, to be raised for larger data.

## Live source snapshots and destination coordination

The user explicitly requires copying an open or actively running conversation.
There is no close-first prerequisite, maintenance mode, or source writer lock.
This supersedes the original exclusive-source-lock design.

Codex's `thread-store/src/local/paginated_fork.rs` persists its own recorder and
captures a fixed history position. Its in-process lifecycle reservation differs
from the cross-process writer lock, which is held throughout a loaded thread.
Hcorral cannot flush another process's in-memory queue. Instead, it copies a
snapshot of complete **saved records**, preserving the existing session ID:

1. Read the authoritative SQLite selection, including committed WAL data.
2. Open the selected rollout with the existing rooted, no-symlink checks and
   retain that descriptor through export. Capture its size before reading.
3. For plain JSONL, find the last newline within that size. An unfinished final
   record is excluded; malformed complete records still fail validation. Size
   limits also apply to the unfinished tail. Compressed representations require
   a valid bounded compressed stream, not a guessed incomplete-frame cutoff.
4. Validate and hash precisely those bytes, then resolve inherited rollouts using
   their exact byte and ordinal boundaries. Parents may keep appending; their
   later history is not copied. Retain their descriptors too.
5. Recheck SQLite selection after capture. A changed selection is an explicit
   retryable copy error, rather than silently mixing a revert/migration with the
   earlier selected data.
6. Stream from the pinned descriptors up to the captured boundaries, verifying
   hashes again before emitting the completion record. Appends are ignored;
   archive, rename, deletion and atomic replacement cannot redirect an open
   descriptor. In-place rewriting or truncation that changes captured bytes
   prevents successful completion and destination publication.

The selected main file remains a resumable conversation, not an inherited-only
prefix. Nothing is written to the source, including lock files. A very new thread
with no complete metadata record yet gets a retry message. A snapshot need not
include an unsaved token, finish the active turn, or remain equal to the source
after copying. It is not a merge or live synchronization mechanism.

Destination publication retains Codex's `.coordination.lock` and
`thread-writer-locks/<UUID>.lock` protocol for required thread/rollout IDs.
Existing divergent history is a conflict; a loaded destination is refused rather
than modified behind its running process. Unrelated open conversations do not
block a new import. Cancellation releases the guards; unlocked entries remain
for Codex's coordinated cleanup. Rust's Unix locks use `flock`, matching Go's
implementation; native qualification must continue to check that contract.

### Native live-copy qualification

`TestNativeCodexCopiesDuringRunningTurn` holds a real Codex model request open at
a loopback provider, copies before allowing the reply to complete, resumes the
copy in the peer version, and proves the original continues afterward. It covers
legacy, paginated and inherited history in both 0.160.0/0.160.1 directions.
The same fixture uses public Docker import/re-export in endpoint qualification.
The broader resume fixture now re-exports native-written history while its source
remains loaded. Unit fixtures cover unfinished tails, oversized or malformed
records, appends, archive/replacement/deletion, changed SQLite selection and
failure without publication after an in-place rewrite or truncation.

### Filesystem boundary

Real Colima qualification found that both the Intel VZ and ARM QEMU file-sharing
mounts allowed a container export while the Mac held the conversation's native
writer lock. A successful `flock` in each kernel is not cross-kernel exclusion.
The ordinary named-volume, daemon-local bind and native-host cases are separate:
they do not require exporting a single locking namespace through a VM share.

The core checks `fstatfs` on opened roots and descendant descriptors, including
history, staging, destination writer locks and separately selected SQLite storage.
Source snapshots never open the writer-lock directory. An unsupported mount only
under that unused source directory therefore does not block export. Linux
rejects FUSE (including virtiofs and SSHFS), 9p, NFS and SMB/CIFS. macOS rejects
non-local mounts and recognized FUSE/virtiofs/9p types. Inspection errors fail
closed. A nested shared mount cannot inherit approval from a local parent.
The check uses the actual descriptor, preserving rooted path/identity handling;
it neither executes a host `stat` command nor guesses from a Docker context name.

These are deliberately unsupported storage classes, not a claim that every NFS
or FUSE implementation has broken locks. Linux's [flock documentation](https://man7.org/linux/man-pages/man2/flock.2.html)
describes remote-lock behavior depending on server, kernel and mount options.
The [FUSE implementation](https://github.com/torvalds/linux/blob/master/fs/fuse/inode.c)
reports one filesystem magic for its different implementations, while
[Lima's mount documentation](https://lima-vm.io/docs/config/mount/) describes its
distinct sharing drivers. Qualifying a class requires evidence for all supported
participants, not merely a second process in the same kernel.

There is no `--force` override or automatic storage migration. Use daemon-local
container history and a native local host home with the existing streaming
transfer. Refusal does not start or replace the workstation. Destination setup
can create its requested empty directory, and a transfer discovering an
unsupported nested path can already have created private staging or native lock
entries; it must not publish through that unsupported path. Existing history is
preserved. The filesystem check does not prove absence of older writers, detect
all layered/exported storage or qualify arbitrary network filesystems. Those
runtime/storage boundaries remain explicit requirements.

The native lifecycle fixture now also holds a Go destination publication guard while asking
Codex to archive, unarchive, resume or delete legacy/paginated history. Each request must
report an existing writer and preserve both bytes and authoritative selection;
the same request must succeed after the guard is released. This passes locally
with native 0.160.0 and 0.160.1 and is included in the platform qualification
runner. It establishes those operations for that runtime pair, not interoperability
with older writers, arbitrary editors, every maintenance operation or an
unqualified network filesystem.

A separate native revert fixture completes two real turns against the loopback
provider, reverts before the second, then transfers the resulting history and
resumes it. It verifies the authoritative rollout changes while the thread ID
stays the same, and excludes the reverted continuation from both the exported
stream and the next model request. Snapshots before and after revert succeed while the native thread remains
loaded; the latter must select the replacement history. The public app-server
revert operation requires a loaded thread; requesting it on an unloaded thread
returns `thread not found`, which is not evidence of writer exclusion. The
existing resume-while-guarded fixture checks the opposite lock direction.

Native Git-metadata checks distinguish the two storage modes. Legacy updates
append rollout metadata and must refuse a held transfer guard without partially
changing SQLite. Paginated updates may succeed while guarded because they
change only database metadata; the selected rollout and its bytes stay intact.
Both modes must update successfully after release and remain transferable.

Native maintenance fixtures exercise compression and background legacy-to-
paginated migration. Each must complete work on an unrelated conversation while
leaving a transfer-locked one untouched, then process the formerly guarded
conversation after release. Compression completion is established by its real
output and released maintenance lock; migration must record the guarded rollout
as busy and later clear that entry. Transfer verifies the resulting native
history. These fixtures use actual Codex workers in disposable homes, not Go
stand-ins for the native lock protocol.

## Selective inherited history

The inspector reads only the exact prefix required from each ancestor. It
validates both byte and ordinal boundaries and follows nested dependencies.
It does not export a parent's subsequent private conversation, flatten logs,
rewrite IDs, or treat `forked_from_id` alone as a required history dependency.

Native tests establish a candidate placement under
`archived_sessions/.hcorral-history/<rollout-id>/<canonical-filename>` for new
prerequisite prefixes. On a fresh Codex home, the requested fork/revert appears
in its appropriate picker and resumes the correct inherited context; a same-ID
ancestor does not become the selected rollout. Codex recursively backfills this
directory: the native 0.160.1 fixture registered a different-thread prerequisite
as an archived SQLite row, although its archived picker omitted the nested file.
That is not a promise of invisibility to other readers. Transfer results must
report those prerequisites and explain their limited history.

An archived main conversation stays archived. Native Codex requires an explicit
`codex unarchive <ID>` before resume; the test exercises the equivalent app-server
operation, not an automatic import action. Native 0.160.0 and 0.160.1 tests now
cover fresh homes and a running app server whose initial backfill has already
completed. An existing complete ancestor can satisfy a matching required prefix
without being truncated or replaced. The inspector excludes managed prefixes
when selecting a complete main conversation, allowing an imported revert to be
re-exported before its destination SQLite index exists.

A later fork may require a longer prefix of the same ancestor. Its existing
managed prefix is validated in full against the incoming bytes, including the
last ordinal and byte boundary. After all conversation conflicts have passed,
the complete longer prefix is staged, synced and atomically renamed over the
managed prerequisite while writer guards remain held. Open readers keep their
original inode; new readers get a complete longer file. Plain and Zstandard
representations are both extended when present, since native lineage lookup
can select either. Existing ownership and Unix mode bits are preserved. The extra
tail is exactly what the new child inherits, not the parent's full continuation.

No ordinary conversation file is extended or replaced. Reimporting the shorter
child reuses the longer prerequisite without shortening it. Results distinguish
created, reused and extended files. A later handled failure leaves a completed
compatible extension intact and explains that retry is safe; it cannot invalidate
any preexisting dependent's shorter cutoff.

When the complete parent is explicitly imported later, every existing managed
representation is validated against the complete incoming bytes. A matching
shorter prefix grows atomically; a divergent or longer prefix is a conflict.
The complete parent is also published at its ordinary active/archived location,
so it is no longer classified only as a prerequisite. Existing children keep
their exact byte/ordinal cutoffs. A repeated full-parent import is a no-op.
Results distinguish this promotion from a child's compatible prefix extension.

## Destination selection and initial indexing

Native initial backfill does not take thread writer locks. It commits a
`running` state before collecting rollout paths, then `complete` after its
metadata upserts. An index scan between prerequisite and main publication can
therefore select the prerequisite. Thread writer guards exclude cooperating
resumes during publication, but do not prevent this metadata observation.

After linked histories or a promoted parent are durable, the importer observes
backfill state and selected-thread metadata in one read-only transaction. It
waits at most 15 seconds for overlapping indexing or database initialization,
honoring cancellation. No database or a pending scan with no prerequisite
selected needs no repair: a later initial scan sees the complete publication.
Completed indexing with an absent row can use native filesystem fallback. A
different ordinary selected conversation is a conflict, never an automatic
replacement. The readiness deadline does not limit large-history validation.

A selected managed prerequisite must belong to the validated incoming lineage.
Only then can the importer repair that requested thread's selected path and
archive fields, after all its complete history is installed. An existing wrong
selection is reserved before live publication; one discovered after publication
is revalidated before repair. Slow history validation/recompression happens
before taking the database write reservation. Writer guards remain held through
the final selection check and repair.

This narrow adapter opens only an existing `state_5.sqlite` in read-write mode
and uses `BEGIN IMMEDIATE`. It requires completed initial indexing, migration 58
with its native checksum, known column types/primary key and known thread trigger
definitions. The tested native versions are 0.160.0 and 0.160.1. A schema change
requires qualification. The update rechecks the expected old selection and
database inode; it changes only `rollout_path`, `archived` and `archived_at`.
Titles, unrelated rows and other metadata are preserved. No database is created,
migrated or copied, and source metadata stays read-only. Unknown layouts or
unconfirmed commits return errors with explicit retry information.

Native tests cover promotion after indexing, plain/compressed prerequisites,
archive preservation, index startup after the first live file, and retry after
interruption at that boundary. Actual native resume verifies both the complete
parent's continuation, the unchanged inherited boundary of its existing child,
and the full history inherited by a new native fork after promotion.
These results do not make prerequisite rows invisible to all database readers.
Before successful completion, or after a failed attempt, partial prerequisites
can be indexed as archived rows; they are not complete standalone parents.
Retry the import to completion before using its requested conversation.

## Transfer stream and publication

Protocol 1 is a tar stream with a bounded `manifest.json`, numbered regular JSONL
payloads, and `complete.sha256`. The manifest identifies the selected thread,
ordered rollout IDs, decoded lengths, hashes, timestamps and relative target
paths. It does not carry source absolute paths, archive ownership, credentials,
configuration or database contents. Unknown manifest fields/protocols fail
explicitly; unknown fields within native rollout records remain byte-preserved.

Export retains its source writer guards through streaming. It decompresses only
the selected bytes, checks the planned SHA-256 and length, and emits completion
only after every payload verifies. The receiver accepts only the expected
regular members and exact order, validates native metadata and lineage again,
and requires the completion checksum, both tar end blocks and transport EOF.
No shell tar extractor writes into live session storage.

The receiver creates a random private `.hcorral-transfer-v1-<nonce>` directory in
the destination home. Payload files are 0600, staged directories 0700, and data
is flushed before publication. Timestamps are preserved to microsecond precision
by descriptor-based operations. Existing directory modes are unchanged. Staged
files belong to the receiving user; archive UID/GID values are never applied.

Publication acquires destination writer guards for every affected thread and
rollout identity, then checks all conflicts before installing any live file.
Identical active/archived/plain/compressed representations can be reused. A full
existing ancestor is compared only through the required prefix and remains
untouched. A different selected main rollout or different bytes under the same
identity cause a conflict. Missing authoritative destination files are not
silently repaired by choosing another history.

New files are installed with exclusive same-filesystem hard links, prerequisites
first and the main rollout last. An existing complete conversation cannot be
overwritten; only the verified extension of a managed prerequisite described
above uses atomic replacement. Newly created parent directory entries and
installed file entries are synced. Once installed, fully validated files are
retained even on a handled failure: a concurrent native indexer may already have
recorded their paths, so deleting them could leave dangling authoritative rows.
Errors report retained files or compatible extensions; a retry verifies/reuses
them and completes any required selection repair. Empty created directories may
remain. Closing the incoming transfer removes only its private staging.

This is not an atomic transaction across several rollout paths. SIGINT/SIGTERM
and ordinary transport errors clean unpublished staging; SIGKILL or machine
failure can leave private staging as well as published prerequisites. A subsequent
import recovers recognized abandoned staging as described below, then validates
and reuses any published history through the normal conflict/selection protocol.

## Abandoned staging recovery

Staging uses its own versioned ownership namespace, independent of the transfer
wire format. A permanent private `.hcorral-staging.lock` coordinates creation,
recovery and close, but is not held while streaming or validating history. The
new directory's empty `.lease` file is exclusively locked before releasing that
coordinator. The kernel keeps the lease until close or process death, so an old
directory or reused PID cannot make an active transfer eligible for cleanup.
Coordination waits honor cancellation; close uses a separate five-second cleanup
budget and leaves recoverable staging if it cannot acquire coordination.

Only a correctly named version-1 directory owned by the effective receiving UID
and with mode 0700 is considered. Recovery skips live leases and preserves
foreign/inaccessible directories, unversioned development staging, future versions,
unrecognized entries, symlinks, special files and altered or hard-linked leases.
It validates the entire flat layout before unlinking any files, using pinned
directory descriptors, non-following opens and bounded enumeration. Cleanup
does not recurse or read conversation/credential contents. The coordinator is
never unlinked, which preserves its lock identity for waiting processes.

Creation syncs the lease and directory before any payload can be created.
Cleanup syncs payload removal before unlinking the lease, then syncs directory
removal. Thus an empty lease-free directory is a recoverable creation/cleanup
interruption; payloads without a lease are unfamiliar and preserved. These
durability rules require the filesystem to honor file and directory syncs and
the advisory locks used by the protocol. Arbitrary external edits to coordination
files are outside the cooperating-writer contract.

Once close or recovery holds the coordinator and has established ownership,
it closes the stage lease before unlinking it. The coordinator still excludes
other cleanup operations. Keeping that descriptor open through unlink can leave
a FUSE hidden file and prevent directory removal; see the documented
[libfuse open-file deletion behavior](https://libfuse.github.io/doxygen/structfuse__config.html).
This ordering change does not establish cross-host lock coherence for arbitrary
shared filesystems; that remains a separate storage qualification requirement.

Recovery discards incomplete private staging rather than trusting it as a source
of resumable data. It never removes a published path, including another hard link
to a staged payload. Tests kill real processes during creation, after staging,
after prerequisite publication and after full publication. A concurrent receive
preserves their live staging; a retry after confirmed SIGKILL removes the orphan
and preserves the inode/content of any published file. The actual bundled Linux
helper also has a killed-import/retry test. These are process-crash tests, not
simulated power failures or qualification of every remote filesystem.

## Internal helper

`cmd/hcorral-session` supplies a shell-free endpoint for this implementation:

```text
hcorral-session protocol
hcorral-session export --protocol=1 --home /absolute/codex-home --sqlite-home /absolute/state-home --id UUID
hcorral-session import --protocol=1 --home /absolute/codex-home --sqlite-home /absolute/state-home --id UUID
```

The protocol probe reports its protocol and compiled OS/architecture without
opening a home. Export writes the stream to stdout. Import reads stdin and writes
one JSON result to stdout; errors go to stderr. The receiver checks that the
manifest thread is the explicitly requested UUID before publication. An import
may create the selected destination home, but a missing source or relocated
SQLite directory is not initialized. Effective SQLite location discovery belongs
to the endpoint integration and cannot be inferred from the default home.

The helper exposes explicit record/file/manifest byte limits and a lineage-file
limit. It sets a private umask and handles cancellation of blocked stdio pipes;
signal tests exercise the command entrypoint in real subprocesses. No credentials,
shell startup, Codex execution, Docker access or network is part of this helper.
The build now prepares Linux payloads for selection by actual container image
architecture, as described below, and the public launcher uses that bundle.

## Endpoint transport and bundled helpers

`internal/sessiontransport` implements a disposable helper container for both
running and stopped workstations. Using one transport avoids injecting files into
the workstation's writable layer and gives cancellation a separately stoppable
remote process. Target inspection requires the actual image ID, numeric runtime
UID/GID/groups, runtime home, schema and ownership. The launcher supplies a static
Linux helper selected by the inspected image architecture, not the client host's
architecture. No normal entrypoint, login shell, tmux attachment, image refresh
or workstation start is part of the operation.

Mount selection retains the storage covering the Codex home and its nested state
mounts. An explicitly resolved, relocated SQLite home must also be in persistent
storage. Additional database mounts are narrowed to the database directory and
made read-only for export. For import, they retain their actual deployed access
so a supported selection can be repaired; a deployed read-only mount is never
made writable. Direct database-file/sidecar mounts are retained without adding
unrelated nested workspace mounts. Named-volume subpaths come from Docker's
`HostConfig.Mounts[].VolumeOptions.Subpath` and are preserved when narrowing.
Codex storage retains its deployed access mode, including nested mount semantics.
Export also accepts read-only storage; import requires a writable destination.
Bind paths refer to the daemon; user-supplied host source/destination paths never
become daemon bind mounts. A narrowed volume subdirectory requires Docker's
`volume-subpath` capability and must be qualified with the supported CLI/daemon.

The helper uses the deployed image ID with `--pull never`, the established
numeric identity/groups, no network, dropped capabilities, no new privileges,
no image healthcheck and an explicit SIGTERM stop signal. `volume-nocopy` prevents
image-content initialization. Unused image-declared volumes are covered with
bounded temporary storage instead of anonymous persistent volumes. Selected
mounts and image volume declarations cannot overlap the helper executable.
The executable is supplied using a one-file tar through `docker cp`, outside
the selected persistent mounts. The helper's disposable rootfs is writable;
it is not advertised as a read-only container.

Ownership, mounts and deployed identity are rechecked before helper creation
and again before execution. Cancellation closes the local pipe and separately
stops the helper under a fresh bounded cleanup context. A random name and exact
operation token restrict cleanup to this helper; neither the workstation nor
its volumes is removed. A lost create reply is handled by inspecting that exact
name/token. The public command holds its local project lock throughout discovery
and transfer; waiting for that lock is cancellable. Docker CLI preflight is not
an atomic reservation against another actor deleting the
original container and volume between inspection and helper creation; qualify
and retain this boundary rather than claiming the CLI cannot create a missing
volume under every external race.

The host/controller and helper share the same transfer core. A producer completes
inspection before the consumer initializes its destination. The producer closes
its pinned source descriptors before closing the transport pipe. The receiver
requires EOF and verified completion before taking destination publication locks. This supports identical
source/destination storage, including tested root-symlink aliases, without taking
two incompatible locks on the same file. Shared Docker-volume aliases still need
real endpoint qualification.

A valid import acknowledgement means the destination confirmed publication,
even when the following Docker inspection or helper cleanup fails. The controller
returns that result together with the finalization error. A missing, malformed
or lost acknowledgement must not be reported as an unchanged destination. Export
to the host waits for successful remote source finalization before publishing.
Both result and small configuration-read buffers have enforced size limits,
including when a command runner uses `io.Copy` optimizations.

Configuration discovery can read a specific TOML file through bounded
`docker cp -L <actual-container-id>:<path> -` while the workstation is stopped.
This reads the actual writable layer as well as mounted files; a helper created
from the image would miss such container-local changes. Only a single bounded
regular member is accepted and nothing is extracted into host storage. A missing
file is distinguished from a missing container, access failure or failed Docker
connection, followed by an identity recheck before treating a layer as absent.
The configuration resolver reads only relevant config files and redacts parser
diagnostics that might echo their contents. Local host reads allow intentional
config symlinks, reject special files before reading, and enforce the same size
bound. Neither endpoint loads authentication data to discover configuration.

`build.sh` builds both Linux helpers first, validates their ELF linkage and
architecture, and deterministically compresses them into generated embed assets.
The decoder rejects missing, empty, corrupted, truncated or concatenated payloads.
The bundle test runs the native architecture's actual executable and inspects the
other one. Ordinary source-only builds can compile without generated helpers,
but transfer then reports the missing payload. Public command integration makes
the package and its embed data reachable. A final-executable gate additionally
requires both exact validated compressed payloads to appear in each launcher;
prepared assets alone cannot satisfy it. Native platform and final release-package
qualification remain outstanding.

Docker contracts checked against the primary sources: [volume population and
subdirectories](https://docs.docker.com/engine/storage/volumes/) and
[Engine mount types](https://github.com/moby/moby/blob/master/api/types/mount/mount.go).

## Public commands and configuration boundary

The command interface is `hcorral session export/import <UUID> [host-codex-home]`.
The optional path is a Codex home root, with precedence over the captured host
`CODEX_HOME` and then `$HOME/.codex`. Host-relative paths use the caller's working
directory; the host remains the Docker client machine when using a remote daemon.
Transfer flags can appear before or after the ID/path. `session --help` needs
neither Docker nor an existing workspace.

SQLite selection has its own `--host-sqlite-home` and `--container-sqlite-home`
options. An explicit host SQLite path can be relative to the caller; the container
option must be absolute. Explicit selection bypasses config discovery. Otherwise
the resolver follows the researched Unix base-file contract: default Codex home,
trimmed `CODEX_SQLITE_HOME`, system config, user config, system requirements, and
legacy managed config, with the last applicable value winning. Relative config
paths resolve against their file's directory; relative environment paths use
the current host caller directory or the deployed container workdir.

Project configuration can override this value only when enabled by Codex's trust
and project-root rules. The resolver detects ancestor `.codex/config.toml` files
declaring `sqlite_home` and requires explicit selection rather than guessing those
rules. It may conservatively require a path for a candidate outside the active
project root. A local requirement that fixes the final SQLite home takes priority
over these project candidates. A missing layer is distinct from permission or
parse failures; those errors do not silently select the default database.

The resolver deliberately does not fetch cloud policy, consult macOS managed
preferences, infer a CLI-selected profile-v2 file or inspect another process's
runtime flags/environment. If such settings change SQLite location, the caller
must provide its effective path explicitly. The same applies to a relative
database path selected from a different working directory when the conversation
was created. This is a documented input boundary, not a claim of full Codex
configuration-engine compatibility. Native 0.160.0/0.160.1 tests verify the default,
relative environment, and user-config-over-environment cases; local managed-file
precedence has source inspection and controlled tests.

The command verifies ownership and explicit state selections before configuration
reads, then transfer rechecks the deployed identity and mounts. It dispatches
before Compose rendering, GUI preparation or normal workstation lifecycle paths.
Human and JSON reports include the resolved SQLite locations and confirmed result.
Human paths are quoted; both modes explain that workspace files, credentials,
configuration, external resources and database-only names/metadata are excluded.
No saved execution policy is automatically run, and archived sessions remain
archived. A nonzero exit may accompany a confirmed publication when finalization
fails; absence of a result does not prove nothing was written.

## Remaining consistency questions

The source no longer requires proof that its writers are idle or participating in
a locking protocol. It relies on saved append-only records, immutable inherited
boundaries and atomic representation replacement, as used by the qualified Codex
versions. Concurrent destructive edits cannot be made safe by a read-only copy;
changed captured bytes fail the export checksum instead of being published.
Future formats must qualify these properties before being accepted.

Destination publication still relies on the supported native lock and metadata
contracts. Arbitrary programs that ignore those contracts are not coordinated;
this is not a reason to require closing the source. Recognized shared-filesystem
classes remain refused, including nested paths. Supporting them would require a
separate read-consistency and destination-publication qualification.

Source/destination aliases do not contend on source writer locks. Existing
identical files are reused; independent or longer main histories conflict rather
than being merged. Recognized abandoned private staging is recovered; older or
unfamiliar staging is never automatically removed.

## Qualification status

The current test policy is defined in the [coverage map](../tests/README.md).
Comprehensive tests run once on Linux, ARM64 receives focused execution checks,
and macOS receives artifact checks only. The four-platform results below are
historical evidence, not the current CI matrix.

The public commands, helper packaging, streaming and cancellation, actual mount
and identity discovery, non-overwriting publication and native resume composition
are implemented. The full nonpublishing matrix passed at `2b41cc7` on Linux and
macOS AMD64/ARM64, with current/pinned Compose on Linux and both Colima hosts.
It covers running/stopped preservation, path/configuration and numeric ownership
cases, storage aliases, connection-fault recovery and both pinned Codex version
directions. The ARM Mac uses an emulated AMD64 guest; Linux ARM64 jobs separately
qualify the native ARM64 helper. See the [execution ledger](implementation-status.md)
for exact job links and the supported boundaries.

Live-copy changes require their own native and endpoint qualification; the older
all-green matrix below predates removal of source locks.
The broader proposal also retains physical-desktop acceptance and qualification
of exact final versioned release artifacts. This development result does not
authorize publication or real-state migration. New Codex versions, metadata
support or storage classes require their own reviewed compatibility evidence.

`HCORRAL_TEST_CODEX=/absolute/path/to/codex go test -v ./internal/session -run
'Test(Native)?Codex'` runs the optional native tests, including promotion and
index overlap. Set
`HCORRAL_TEST_CODEX_PEER` to a second executable for a cross-version round trip;
otherwise the same executable is used at both endpoints. It uses disposable
homes, no credentials and a loopback mock provider. It checks the core stream and
publication, picker visibility, selected rollout, actual resumed model context,
destination writer exclusion, live source copying, and re-export of a completed
native-written turn while its source remains loaded. That core-only
invocation does not exercise Docker transport. The separate public Docker/native
composition in `tests/qualification/codex-sessions.sh journey` does, using the same pinned
versions and synthetic conversations. Neither suite uses a real model service or
claims that all metadata and external resources are portable.
