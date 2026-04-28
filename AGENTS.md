## Project Notes
- Go + GTK 4 + libVLC radio player.
- Stream metadata: libVLC media metadata, keep polling light.
- Builds/tests: use normal Go cache; never set `GOCACHE=/tmp/...`.
- CGO/gotk4 builds are slow without cache; request escalation if cache access is blocked.
- Prefer `go test ./...` for verification.
