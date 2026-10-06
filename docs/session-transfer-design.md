# Session transfer implementation decisions

Design reference: myCodex `.proposals/hcorral-go-successor.md`, phase 5, and
`.proposals/codex-session-transfer.md`. This is an implementation record, not
a claim that `hcorral session` is available yet.

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

SQLite queries use `mode=ro`, a read transaction, query-only mode and a bounded
busy timeout. They deliberately do not use `immutable=1`, which could omit
committed WAL records. Only the requested thread's selected path, history mode
and archive status are queried. Source databases and sidecars are never part
of a transfer plan. The native SQLite VFS opens its own descriptors: checked
database/sidecar paths and database identity validation supplement the rooted
rollout I/O, but this is not an atomic filesystem snapshot against arbitrary
same-user file replacement. Qualify that boundary before destination writes.

The inspection API requires the effective SQLite home explicitly, allowing a
separate state directory. Endpoint code still needs to resolve Codex's actual
configuration/environment precedence or reject unsupported overrides clearly.
It must not infer that state always lives under `CODEX_HOME`.

Ambiguous rollout IDs without authoritative selection fail. Plain/compressed
representations can be deduplicated only when their decoded selected bytes
match. Unknown database generations fail; older unused databases alongside a
supported current database do not override it. A missing, inconsistent or
out-of-root authoritative selection fails without a speculative fallback.

Plans contain SHA-256 hashes, decoded sizes, modification times, native
metadata and required files in prerequisite-first order. Compressed logs are
decoded to byte-identical JSONL. The parser does not have Scanner's small
default line limit. Explicit resource limits currently default to 256 MiB per
record, 64 GiB per decoded file and 4,096 lineage files; the transport must
expose an intentional way to raise these for valid larger data.

## Writer coordination

`Snapshot` takes Codex's `.coordination.lock` while opening/acquiring each
`thread-writer-locks/<UUID>.lock`. Both stable thread and immutable rollout IDs
are guarded where they differ. Locks remain held after inspection so payload
streaming can use the same guards. New unrelated writers are not blocked for
the full copy; an existing owner of a required thread causes an immediate
busy error. Cancellation/failure releases acquired descriptors.

This requires a writable lock namespace at the source even though conversation
files and SQLite tables are read-only. Stopped-container export therefore
cannot promise a completely read-only volume mount with this protocol. No
credentials, startup files or normal entrypoint should be involved. Unlocked
lock-file entries can remain for Codex's own coordinated stale-lock cleanup.
Their existence is not evidence that a writer is active.

This coordinates writers implementing the researched protocol. It does not
make a pre-protocol Codex process or arbitrary file editor safe. Runtime/version
qualification and the shared-volume writer boundary remain integration gates;
the recorded creation version of a rollout alone is insufficient evidence of
which executable can currently write it.
Rust currently implements its Unix file locks with `flock`, matching the Go
guards; keep this interoperability check in runtime qualification rather than
assuming it permanently. See [Rust File::try_lock](https://doc.rust-lang.org/std/fs/struct.File.html#method.try_lock).

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

The receiver creates a random private `.hcorral-transfer-<nonce>` directory in
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
first and the main rollout last. Existing paths cannot be overwritten. Newly
created parent directory entries and installed file entries are synced. On a
handled failure, reverse cleanup removes only links whose inode still belongs
to this attempt; replacements and preexisting files are preserved. Empty created
directories may remain. Closing the incoming transfer removes its private
staging, never the successful installed files.

This is not an atomic transaction across several rollout paths. SIGINT/SIGTERM
and ordinary transport errors run cleanup; SIGKILL or machine failure can leave
private staging and already-published prerequisite prefixes. Do not recursively
delete every matching staging directory: another transfer may own it. A cleanup
or retry protocol for those interrupted states remains part of final integration.

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
Its Linux binaries still need to be packaged with the launcher and selected using
the actual container architecture.

## Remaining consistency questions

The tested initialized-home case is not proof against a new native initial
backfill starting midway through publication. The researched backfill does not
take thread writer locks and recursively indexes prerequisite prefixes. A same-ID
prefix observed before the main rollout can become an authoritative SQLite row.
Resolve and test that race before claiming general concurrent import support;
the implementation currently refuses a preexisting prefix selection and does not
write SQLite to repair it.

Likewise, a longer required prefix or a complete parent imported after an earlier
partial prerequisite currently conflicts. That preserves existing data, but is
not the final answer for compatible extensions: qualify a way to preserve both
existing dependents and the newly requested complete history without making a
short prefix the selected complete conversation. Source/destination aliases
also require endpoint-level detection before acquiring both sets of locks.

## Remaining implementation

1. Resolve concurrent initial backfill, compatible prefix extension/promotion
   and recovery after an uncatchable interruption. Keep database selection and
   prerequisite visibility explicit.
2. Resolve effective endpoint paths and SQLite configuration, storage aliases,
   source/destination identity, and result reporting including saved policy and
   optional metadata/resource limitations.
3. Package the Linux helpers and implement Docker streaming/cancellation using
   actual deployed home/mount/identity and architecture inspection.
4. Wire public `session export/import`; qualify running/stopped and remote-Docker
   paths without workstation pull/start/recreate/attach side effects.
5. Extend native fixtures, runtime writer qualification and platform coverage,
   then execute the full endpoint and source/destination acceptance matrix.

`HCORRAL_TEST_CODEX=/absolute/path/to/codex go test -v ./internal/session -run
TestCodexResumesNativeHistory` runs the optional native test. Set
`HCORRAL_TEST_CODEX_PEER` to a second executable for a cross-version round trip;
otherwise the same executable is used at both endpoints. It uses disposable
homes, no credentials and a loopback mock provider. It checks the core stream and
publication, picker visibility, selected rollout, actual resumed model context,
writer exclusion, and re-export of a completed native-written turn. It does not
yet qualify Docker transport, a real model service or all metadata portability.
See `implementation-status.md` for the executed version matrix.
