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
- Image-declared anonymous volumes do not become persistent transfer side effects.

The fixture carries runtime ownership labels but bypasses a workstation
entrypoint. These tests establish the Docker transport contract, not real-image
shell initialization or native Codex resume. Those require the image canaries
and native Codex tests. A local daemon run does not establish network-disconnect
behavior, even though host files and Docker volume files use separate storage.

The tests retain the invoking Docker context/configuration and use fresh host
homes and uniquely named Docker resources. Run against a development daemon
without concurrent volume creation/removal: the volume inventory check is
deliberately exact. Existing user sessions and credentials are never used as
fixtures. Both Linux architectures run this suite in CI; the release Colima
qualification also invokes it where supported.
