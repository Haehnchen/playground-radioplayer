## Project Notes
- Go + GTK 4 + GStreamer radio player.
- Stream metadata: GStreamer tags/caps, keep polling light.
- Builds/tests: use normal Go cache; never set `GOCACHE=/tmp/...`.
- CGO/gotk4 builds are slow without cache; request escalation if cache access is blocked.
- Prefer `go test ./...` for verification.
