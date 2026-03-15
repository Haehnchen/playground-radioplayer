package main

/*
#cgo pkg-config: libvlc
#include <stdlib.h>
#include <vlc/vlc.h>
*/
import "C"
import (
	"bufio"
	"crypto/sha256"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

//go:embed icon.png
var iconFS embed.FS

type Track struct {
	Name string
	URL  string
}

type Player struct {
	window        fyne.Window
	playlist      []Track
	filteredList  []Track
	list          *widget.List
	volume        *widget.Slider
	volumeLabel   *widget.Label
	currentTrack  *widget.Label
	searchEntry   *widget.Entry
	instance      *C.libvlc_instance_t
	mediaPlayer   *C.libvlc_media_player_t
	media         *C.libvlc_media_t
	playingIdx    int
}

func installDesktopEntry(iconData []byte) {
	if runtime.GOOS != "linux" {
		return
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	iconDir := filepath.Join(home, ".local", "share", "icons", "hicolor", "256x256", "apps")
	iconPath := filepath.Join(iconDir, "radioplayer.png")
	desktopDir := filepath.Join(home, ".local", "share", "applications")
	desktopPath := filepath.Join(desktopDir, "radioplayer.desktop")

	os.MkdirAll(iconDir, 0755)
	os.MkdirAll(desktopDir, 0755)

	// Hash-Check: Icon nur überschreiben wenn es sich geändert hat
	newHash := sha256.Sum256(iconData)
	if existing, err := os.ReadFile(iconPath); err == nil {
		existingHash := sha256.Sum256(existing)
		if existingHash == newHash {
			return // Icon unverändert, nichts zu tun
		}
	}

	os.WriteFile(iconPath, iconData, 0644)

	exe, err := os.Executable()
	if err != nil {
		exe = "radioplayer"
	}

	desktop := fmt.Sprintf(`[Desktop Entry]
Name=Radio Player
Comment=Simple Radio Player
Exec=%s %%f
Icon=radioplayer
Terminal=false
Type=Application
Categories=AudioVideo;Audio;
StartupWMClass=radio player
`, exe)

	os.WriteFile(desktopPath, []byte(desktop), 0644)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: player <m3u8-file>")
		os.Exit(1)
	}

	filename := os.Args[1]
	tracks, err := parseM3U8(filename)
	if err != nil {
		fmt.Printf("Error loading playlist: %v\n", err)
		os.Exit(1)
	}

	instance := C.libvlc_new(0, nil)
	if instance == nil {
		fmt.Println("Failed to initialize VLC. Install with: sudo apt install libvlc-dev vlc")
		os.Exit(1)
	}

	a := app.NewWithID("io.github.daniel.radioplayer")
	w := a.NewWindow("Radio Player")

	// Load embedded icon
	iconData, err := iconFS.ReadFile("icon.png")
	if err == nil {
		iconRes := fyne.NewStaticResource("icon.png", iconData)
		w.SetIcon(iconRes)
		installDesktopEntry(iconData)
	}

	p := &Player{
		window:       w,
		playlist:     tracks,
		filteredList: tracks,
		instance:     instance,
		playingIdx:   -1,
	}

	p.setupUI()
	w.Resize(fyne.NewSize(600, 700))
	w.ShowAndRun()

	if p.mediaPlayer != nil {
		C.libvlc_media_player_release(p.mediaPlayer)
	}
	if p.instance != nil {
		C.libvlc_release(p.instance)
	}
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
				name = extractName(line)
			}
			tracks = append(tracks, Track{
				Name: name,
				URL:  line,
			})
			currentName = ""
		}
	}

	return tracks, scanner.Err()
}

func extractName(url string) string {
	base := filepath.Base(url)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
}

func (p *Player) setupUI() {
	title := widget.NewLabelWithStyle("Radio Player", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	title.Importance = widget.HighImportance

	p.currentTrack = widget.NewLabelWithStyle("Wähle einen Sender", fyne.TextAlignCenter, fyne.TextStyle{Italic: true})
	p.currentTrack.Wrapping = fyne.TextTruncate

	p.volumeLabel = widget.NewLabelWithStyle("75%", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	p.volume = widget.NewSlider(0, 100)
	p.volume.SetValue(75)
	p.volume.Step = 1
	p.volume.OnChanged = func(val float64) {
		v := int(val)
		p.volumeLabel.SetText(fmt.Sprintf("%d%%", v))
		p.setVolume(v)
	}

	volumeDown := widget.NewButtonWithIcon("", theme.ContentRemoveIcon(), func() {
		newVal := p.volume.Value - 5
		if newVal < 0 {
			newVal = 0
		}
		p.volume.SetValue(newVal)
	})

	volumeUp := widget.NewButtonWithIcon("", theme.ContentAddIcon(), func() {
		newVal := p.volume.Value + 5
		if newVal > 100 {
			newVal = 100
		}
		p.volume.SetValue(newVal)
	})

	volumeRow := container.NewBorder(nil, nil, volumeDown, volumeUp,
		container.NewVBox(p.volumeLabel, p.volume),
	)

	p.searchEntry = widget.NewEntry()
	p.searchEntry.SetPlaceHolder("Sender suchen...")
	p.searchEntry.OnChanged = p.filterPlaylist

	stopBtn := widget.NewButtonWithIcon("Stop", theme.MediaStopIcon(), p.stopPlayback)
	stopBtn.Importance = widget.DangerImportance

	p.list = widget.NewList(
		func() int { return len(p.filteredList) },
		func() fyne.CanvasObject {
			return widget.NewLabel("Sender Name")
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			label := obj.(*widget.Label)
			label.SetText(p.filteredList[id].Name)
			if p.isPlayingTrack(p.filteredList[id]) {
				label.TextStyle = fyne.TextStyle{Bold: true}
			} else {
				label.TextStyle = fyne.TextStyle{}
			}
		},
	)

	p.list.OnSelected = func(id widget.ListItemID) {
		p.playTrack(id)
	}

	countLabel := widget.NewLabel(fmt.Sprintf("%d Sender", len(p.playlist)))

	header := container.NewVBox(
		title,
		widget.NewSeparator(),
		p.currentTrack,
		widget.NewSeparator(),
	)

	controls := container.NewVBox(
		container.NewBorder(nil, nil, nil, stopBtn, volumeRow),
		widget.NewSeparator(),
		container.NewBorder(nil, nil, nil, countLabel, p.searchEntry),
		widget.NewSeparator(),
	)

	top := container.NewVBox(header, controls)
	content := container.NewBorder(top, nil, nil, nil, p.list)

	p.window.SetContent(content)
}

func (p *Player) isPlayingTrack(track Track) bool {
	if p.playingIdx < 0 || p.playingIdx >= len(p.playlist) {
		return false
	}
	return p.playlist[p.playingIdx].URL == track.URL
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
	p.list.Refresh()
}

func (p *Player) playTrack(id widget.ListItemID) {
	track := p.filteredList[id]

	for i, t := range p.playlist {
		if t.URL == track.URL {
			p.playingIdx = i
			break
		}
	}

	p.stopPlayback()

	curl := C.CString(track.URL)
	defer C.free(unsafe.Pointer(curl))

	p.media = C.libvlc_media_new_location(p.instance, curl)
	if p.media == nil {
		p.currentTrack.SetText("Fehler beim Laden")
		return
	}

	p.mediaPlayer = C.libvlc_media_player_new_from_media(p.media)
	if p.mediaPlayer == nil {
		p.currentTrack.SetText("Fehler beim Erstellen des Players")
		return
	}

	p.setVolume(int(p.volume.Value))
	C.libvlc_media_player_play(p.mediaPlayer)
	p.currentTrack.SetText(fmt.Sprintf("Spielt: %s", track.Name))
	p.list.Refresh()
}

func (p *Player) setVolume(vol int) {
	if p.mediaPlayer != nil {
		C.libvlc_audio_set_volume(p.mediaPlayer, C.int(vol))
	}
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
	p.currentTrack.SetText("Gestoppt")
	p.list.Refresh()
}
