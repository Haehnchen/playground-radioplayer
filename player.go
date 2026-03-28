package main

/*
#cgo pkg-config: libvlc
#include <stdlib.h>
#include <vlc/vlc.h>
*/
import "C"

import (
	"bufio"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"
)

func (p *Player) playTrack(id int) {
	if id < 0 || id >= len(p.filteredList) {
		return
	}
	track := p.filteredList[id]

	for i, t := range p.playlist {
		if t.URL == track.URL {
			p.playingIdx = i
			break
		}
	}

	if p.mediaPlayer != nil {
		C.libvlc_media_player_stop(p.mediaPlayer)
		C.libvlc_media_player_release(p.mediaPlayer)
		p.mediaPlayer = nil
	}
	if p.media != nil {
		C.libvlc_media_release(p.media)
		p.media = nil
	}

	curl := C.CString(track.URL)
	defer C.free(unsafe.Pointer(curl))

	p.media = C.libvlc_media_new_location(p.instance, curl)
	if p.media == nil {
		p.statusMsg = "Error loading " + track.Name
		p.playingIdx = -1
		return
	}

	p.mediaPlayer = C.libvlc_media_player_new_from_media(p.media)
	if p.mediaPlayer == nil {
		p.statusMsg = "Error creating player"
		p.playingIdx = -1
		return
	}

	p.setVolume(int(p.volSlider.Value * 100))
	C.libvlc_media_player_play(p.mediaPlayer)
	p.statusMsg = ""
	p.settings.LastTrackURL = track.URL
	saveSettings(p.settings)
}

func (p *Player) stopPlayback() {
	if p.mediaPlayer != nil {
		C.libvlc_media_player_stop(p.mediaPlayer)
		C.libvlc_media_player_release(p.mediaPlayer)
		p.mediaPlayer = nil
	}
	if p.media != nil {
		C.libvlc_media_release(p.media)
		p.media = nil
	}
	p.playingIdx = -1
	p.settings.LastTrackURL = ""
	saveSettings(p.settings)
}

func (p *Player) setVolume(vol int) {
	if p.mediaPlayer != nil {
		C.libvlc_audio_set_volume(p.mediaPlayer, C.int(vol))
	}
}

func (p *Player) toggleMute() {
	if p.isMuted {
		p.isMuted = false
		p.setVolume(p.savedVolume)
		p.volSlider.Value = float32(p.savedVolume) / 100.0
	} else {
		p.isMuted = true
		p.savedVolume = int(p.volSlider.Value * 100)
		p.setVolume(0)
	}
}

func (p *Player) isPlayingTrack(track Track) bool {
	if p.playingIdx < 0 || p.playingIdx >= len(p.playlist) {
		return false
	}
	return p.playlist[p.playingIdx].URL == track.URL
}

func (p *Player) currentStatus() string {
	if p.statusMsg != "" {
		return p.statusMsg
	}
	if p.playingIdx >= 0 && p.playingIdx < len(p.playlist) {
		return "Playing: " + p.playlist[p.playingIdx].Name
	}
	if len(p.playlist) == 0 {
		return "No playlist loaded"
	}
	return "Stopped"
}

func (p *Player) autoPlayLastTrack() {
	if p.settings.LastTrackURL == "" {
		return
	}
	for i, track := range p.playlist {
		if track.URL == p.settings.LastTrackURL {
			p.playTrack(i)
			return
		}
	}
}

func (p *Player) filterPlaylist(query string) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		p.filteredList = p.playlist
	} else {
		var filtered []Track
		for _, t := range p.playlist {
			if strings.Contains(strings.ToLower(t.Name), query) {
				filtered = append(filtered, t)
			}
		}
		p.filteredList = filtered
	}
}

// --- Settings ---

func getSettingsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "radioplayer", "settings.json")
}

func loadSettings() Settings {
	data, err := os.ReadFile(getSettingsPath())
	if err != nil {
		return Settings{Volume: 75}
	}
	var s Settings
	if json.Unmarshal(data, &s) != nil {
		return Settings{Volume: 75}
	}
	return s
}

func saveSettings(s Settings) {
	path := getSettingsPath()
	os.MkdirAll(filepath.Dir(path), 0755)
	data, _ := json.MarshalIndent(s, "", "  ")
	os.WriteFile(path, data, 0644)
}

// --- Playlist parsing ---

func (p *Player) loadPlaylist(filename string) bool {
	var tracks []Track
	var err error

	ext := strings.ToLower(filepath.Ext(filename))
	if ext == ".xspf" {
		tracks, err = parseXSPF(filename)
	} else {
		tracks, err = parseM3U8(filename)
	}

	if err != nil || len(tracks) == 0 {
		return false
	}
	p.playlist = tracks
	p.filteredList = tracks
	absPath, err := filepath.Abs(filename)
	if err != nil {
		absPath = filename
	}
	p.settings.LastFile = absPath
	saveSettings(p.settings)
	return true
}

func parseM3U8(filename string) ([]Track, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var tracks []Track
	var currentName string
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#EXTINF:") {
			parts := strings.SplitN(line, ",", 2)
			if len(parts) == 2 {
				currentName = strings.TrimSpace(parts[1])
			}
		} else if !strings.HasPrefix(line, "#") {
			name := currentName
			if name == "" {
				base := filepath.Base(line)
				name = strings.TrimSuffix(base, filepath.Ext(base))
			}
			tracks = append(tracks, Track{Name: name, URL: line})
			currentName = ""
		}
	}
	return tracks, scanner.Err()
}

type xspfPlaylist struct {
	XMLName   xml.Name   `xml:"playlist"`
	TrackList xspfTracks `xml:"trackList"`
}

type xspfTracks struct {
	Tracks []xspfTrack `xml:"track"`
}

type xspfTrack struct {
	Location string `xml:"location"`
	Title    string `xml:"title"`
}

func parseXSPF(filename string) ([]Track, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	var xspf xspfPlaylist
	if err := xml.Unmarshal(data, &xspf); err != nil {
		return nil, err
	}

	var tracks []Track
	for _, t := range xspf.TrackList.Tracks {
		name := t.Title
		if name == "" {
			name = filepath.Base(t.Location)
		}
		tracks = append(tracks, Track{Name: name, URL: t.Location})
	}
	return tracks, nil
}

// --- File dialog ---

func (p *Player) pickFile() {
	startDir := ""
	if p.settings.LastFile != "" {
		startDir = filepath.Dir(p.settings.LastFile)
	}
	path := openFileDialog(startDir)
	if path != "" {
		p.pendingFile <- path
		p.window.Invalidate()
	}
}

func openFileDialog(startDir string) string {
	if startDir == "" {
		startDir, _ = os.UserHomeDir()
	}
	if _, err := os.Stat(startDir); err != nil {
		startDir, _ = os.UserHomeDir()
	}
	out, err := exec.Command("zenity", "--file-selection",
		"--title=Open Playlist",
		"--file-filter=Playlist Files (m3u, m3u8, xspf)|*.m3u *.m3u8 *.xspf",
		"--file-filter=All Files|*",
		"--filename="+startDir+"/").Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	out, err = exec.Command("kdialog", "--getopenfilename", startDir, "*.m3u *.m3u8 *.xspf|Playlist Files (m3u, m3u8, xspf)").Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}

// --- Desktop entry ---

func installDesktopEntry(iconData []byte) bool {
	if runtime.GOOS != "linux" {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}

	desktopDir := filepath.Join(home, ".local", "share", "applications")
	iconDir := filepath.Join(home, ".local", "share", "icons", "hicolor", "256x256", "apps")
	pixmapsDir := filepath.Join(home, ".local", "share", "pixmaps")

	os.MkdirAll(desktopDir, 0755)
	os.MkdirAll(iconDir, 0755)
	os.MkdirAll(pixmapsDir, 0755)

	os.WriteFile(filepath.Join(iconDir, "radioplayer.png"), iconData, 0644)
	os.WriteFile(filepath.Join(pixmapsDir, "radioplayer.png"), iconData, 0644)

	exe, err := os.Executable()
	if err != nil {
		exe = "radioplayer"
	}
	os.Chmod(exe, 0755)

	desktop := fmt.Sprintf(`[Desktop Entry]
Name=Radio Player
Comment=Simple Radio Player
Exec=%s
Icon=radioplayer
Terminal=false
Type=Application
Categories=AudioVideo;Audio;
StartupNotify=true
StartupWMClass=radioplayer
`, exe)

	if err := os.WriteFile(filepath.Join(desktopDir, "radioplayer.desktop"), []byte(desktop), 0644); err != nil {
		return false
	}

	exec.Command("update-desktop-database", desktopDir).Run()
	exec.Command("gtk-update-icon-cache", "-f", filepath.Join(home, ".local", "share", "icons", "hicolor")).Run()
	return true
}
