# Radio Player

A simple radio player with a GUI, built with Go, Fyne, and libVLC. Uses libVLC inline via CGO (no external VLC process spawned).

## Features

- Play internet radio streams from M3U8 playlists
- Search/filter stations
- Volume control
- Automatically installs desktop entry on Linux

## Requirements

- Go 1.21+
- VLC and libvlc development files
- Fyne dependencies (see below)

### Linux (Debian/Ubuntu)

```bash
sudo apt install libvlc-dev vlc libgl1-mesa-dev xorg-dev
```

### macOS

```bash
brew install vlc
```

### Windows

Install VLC from https://www.videolan.org/vlc/

## Build

```bash
make build
```

## Usage

```bash
./radioplayer
```

```bash
./radioplayer <m3u8-file>
```

## Make Targets

| Target | Description |
|--------|-------------|
| `make build` | Build the binary |
| `make clean` | Remove the binary |
| `make run` | Build and run with default playlist |
| `make install` | Install to `~/.local/bin/` |

## M3U8 Format

The player supports standard M3U8 playlists with `#EXTINF` tags:

```m3u8
#EXTM3U
#EXTINF:-1,BBC Radio 1
https://stream.live.vc.bbcmedia.co.uk/bbc_radio_one
#EXTINF:-1,Jazz FM
https://edge-bauerall-01-gos2.sharp-stream.com/jazz.mp3
```
