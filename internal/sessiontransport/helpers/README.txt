build.sh generates the Linux AMD64 and ARM64 helper payloads here before
building each launcher target. Generated binaries are not committed.

The directory remains embeddable in an ordinary source-only Go build. Such a
build reports missing helpers when transfer is requested; release builds must
include and qualify both payloads from the same source revision.
