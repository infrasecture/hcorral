# Docker acceptance tests

Run `./build.sh`, then `./tests/integration/run.sh`. The build produces the
launcher and a native session acceptance executable under `dist/tests/`.
`HCORRAL_TEST_BINARY` can select an extracted package's launcher. Test executables
are included in the CI transport artifact, not the user archives or packages.
No Go or Python installation is needed to run the session acceptance executable.
The older lifecycle PTY test still uses Python as a test dependency.

`session-transfer.sh` builds a disposable Alpine fixture containing only the
tools needed to hold real filesystem locks. It supplies neither Codex nor a
transfer helper. Tests execute the packaged launcher and its embedded helper
through real Docker, for running and stopped workstations and numeric identities
1000:1000, 501:20 and 12345:23456. They check:

- Host destination precedence, spaces, exact bytes, private permissions and
  receiving-user ownership; client paths are never mounted into the daemon.
- Import, repeated export/import and divergent-content refusal.
- Preservation of authentication, configuration, unrelated conversations,
  workstation identity, lifecycle timestamps, mounts and persistent volumes.
- Read-only storage refusal and a writer lock owned by a separate container.
- Named-volume subpaths and daemon-side binds, including private propagation
  and nonrecursive mounts. The test owns the daemon paths and never assumes a
  client pathname exists there.
- SIGTERM cancellation with a live remote helper blocked on a real destination
  lock, helper removal, preservation of the lock owner and successful retry.
- A real Docker attach connection reset while a helper is blocked, with
  bounded failure, helper removal and successful retry through the original
  endpoint. A second fault suppresses the completion reply after publication:
  the launcher must report an unknown outcome and reuse the complete history
  on retry. A loopback TCP proxy forwards the Engine API to the actual Unix
  Docker endpoint; it never fabricates daemon or helper responses. These fault
  fixtures skip non-Unix upstream endpoints and do not model a prolonged daemon
  outage or an SSH/TLS-specific failure.
- Both directions of a transfer whose host home is a symlink to the same
  client-visible bind mounted by the container. The original file inode,
  content and private mode must survive without a lock deadlock. This fixture
  requires the daemon to see a disposable directory beneath the client home
  (as local Linux Docker and the Colima fixture do). Supported native storage
  must succeed; unsupported VM-shared FUSE/9p storage must fail explicitly,
  preserving the same original file identity and bytes.
- Host-versus-container writer exclusion and retry on native shared storage.
  Unsupported VM sharing must be refused even without a writer, in both
  directions, including an import of previously absent history. A separate
  fixture places just the native writer-lock directory on a shared mount under
  a daemon-local home. The test independently inspects the guest filesystem and
  requires the specific unsupported-storage error, never any arbitrary failure.
- Image-declared anonymous volumes do not become persistent transfer side effects.

The fixture carries runtime ownership labels but bypasses a workstation
entrypoint. The endpoint executable tests the Docker transport contract;
real-image shell initialization requires the image canaries. After those
endpoint tests, the runner invokes `codex-sessions.sh` in public-command mode
against the same daemon, fixture image and packaged launcher. That separate
stage verifies native resume of richer histories and native-written return
transfers with both pinned Codex versions. It requires the native session-core
acceptance executable as well as the endpoint executable. Connection-fault
coverage requires the explicit proxy
cases to execute; ordinary local endpoint success does not establish it. The
proxy plumbing also has a local protocol test for input EOF and reply suppression,
which is not a substitute for the real-daemon tests.

The tests retain the invoking Docker context/configuration and use fresh host
homes and uniquely named Docker resources. Run against a development daemon
without concurrent volume creation/removal: the volume inventory check is
deliberately exact. Existing user sessions and credentials are never used as
fixtures. Both Linux architectures run this suite in CI; the release Colima
qualification also invokes it where supported.
