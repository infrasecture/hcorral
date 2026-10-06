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
operation, not an automatic import action. Native tests currently cover fresh
homes. Existing-destination collision/backfill cases and the older supported
runtime matrix still need qualification before finalizing publication. An
existing complete ancestor may satisfy a matching required prefix, but it must
never be truncated or replaced by that prefix.

## Remaining implementation

1. Finish qualifying prefix placement for existing homes and supported runtime
   versions. Define selection when an imported revert has no SQLite row yet;
   the generic inspector currently rejects its several same-thread rollouts
   until authoritative selection is available.
2. Implement destination conflict checks, private staging, exclusive
   publication, interruption cleanup and idempotent retries.
3. Implement the versioned streaming protocol and target-architecture Linux
   helpers, with cancellation and size/hash verification.
4. Wire `session export/import` through actual deployed home/mount/identity
   inspection; qualify running/stopped and remote-Docker paths.
5. Qualify runtime writer compatibility, optional metadata and external
   attachment limitations, then execute the full source/destination matrix.

`HCORRAL_TEST_CODEX=/absolute/path/to/codex go test -v ./internal/session -run
TestCodexResumesNativeHistory` runs the optional native test. It uses disposable
homes, no credentials and a loopback mock provider. It checks picker visibility,
selected-rollout identity, actual resume, model-visible saved/inherited messages
and exclusion by the native runtime's writer lock. This is native format evidence,
not yet end-to-end transport/publication acceptance. See `implementation-status.md`.
