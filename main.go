package main

/*
#cgo pkg-config: libvlc
#include <stdlib.h>
#include <vlc/vlc.h>
*/
import "C"
import (
	"bufio"
	"embed"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

//go:embed icon.png
var iconFS embed.FS

type Track struct {
	Name string
	URL  string
}

type Settings struct {
	LastFile      string `json:"last_file"`
	LastTrackURL  string `json:"last_track_url"`
	Volume        int    `json:"volume"`
}

func getSettingsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "radioplayer", "settings.json")
}

func loadSettings() Settings {
	path := getSettingsPath()
	data, err := os.ReadFile(path)
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

type Player struct {
	window        fyne.Window
	playlist      []Track
	filteredList  []Track
	list          *widget.List
	volume        *widget.Slider
	currentTrack  *widget.Label
	searchEntry   *widget.Entry
	playStopBtn   *widget.Button
	muteBtn       *widget.Button
	instance      *C.libvlc_instance_t
	mediaPlayer   *C.libvlc_media_player_t
	media         *C.libvlc_media_t
	playingIdx    int
	settings      Settings
	currentFile   string
	isMuted       bool
	savedVolume   int
}

func installDesktopEntry(iconData []byte) bool {
	if runtime.GOOS != "linux" {
		return false
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}

	// Remove ALL old radio player desktop entries
	desktopDir := filepath.Join(home, ".local", "share", "applications")
	oldEntries, _ := filepath.Glob(filepath.Join(desktopDir, "*radio*.desktop"))
	for _, e := range oldEntries {
		os.Remove(e)
	}

	// Remove old icons
	oldIcons, _ := filepath.Glob(filepath.Join(home, ".local", "share", "pixmaps", "*radio*.png"))
	for _, i := range oldIcons {
		os.Remove(i)
	}
	oldIcons2, _ := filepath.Glob(filepath.Join(home, ".local", "share", "icons", "hicolor", "*", "apps", "*radio*.png"))
	for _, i := range oldIcons2 {
		os.Remove(i)
	}

	// Create directories
	pixmapsDir := filepath.Join(home, ".local", "share", "pixmaps")
	iconDir := filepath.Join(home, ".local", "share", "icons", "hicolor", "256x256", "apps")

	os.MkdirAll(pixmapsDir, 0755)
	os.MkdirAll(iconDir, 0755)
	os.MkdirAll(desktopDir, 0755)

	// Write icon to both locations
	os.WriteFile(filepath.Join(pixmapsDir, "radioplayer.png"), iconData, 0644)
	os.WriteFile(filepath.Join(iconDir, "radioplayer.png"), iconData, 0644)

	exe, err := os.Executable()
	if err != nil {
		exe = "radioplayer"
	}

	exeName := filepath.Base(exe)
	desktop := fmt.Sprintf(`[Desktop Entry]
Name=Radio Player
Comment=Simple Radio Player
Exec=%s
Icon=radioplayer
Terminal=false
Type=Application
Categories=AudioVideo;Audio;
StartupNotify=true
StartupWMClass=%s
`, exe, exeName)

	desktopPath := filepath.Join(desktopDir, "radioplayer.desktop")
	if err := os.WriteFile(desktopPath, []byte(desktop), 0644); err != nil {
		return false
	}

	// Make executable
	os.Chmod(exe, 0755)

	// Update caches
	exec.Command("update-desktop-database", desktopDir).Run()
	exec.Command("gtk-update-icon-cache", "-f", filepath.Join(home, ".local", "share", "icons", "hicolor")).Run()

	return true
}

func main() {
	// Disable HiDPI auto-scaling
	os.Setenv("GDK_SCALE", "1")
	os.Setenv("GDK_DPI_SCALE", "1")
	os.Setenv("QT_SCALE_FACTOR", "1")

	instance := C.libvlc_new(0, nil)
	if instance == nil {
		fmt.Println("Failed to initialize VLC. Install with: sudo apt install libvlc-dev vlc")
		os.Exit(1)
	}

	a := app.NewWithID("radioplayer")
	w := a.NewWindow("Radio Player")

	iconData, err := iconFS.ReadFile("icon.png")
	if err == nil {
		iconRes := fyne.NewStaticResource("icon.png", iconData)
		w.SetIcon(iconRes)
	}

	p := &Player{
		window:     w,
		instance:   instance,
		playingIdx: -1,
		settings:   loadSettings(),
	}

	if len(os.Args) >= 2 {
		p.loadPlaylist(os.Args[1])
		p.setupUI()
		w.Resize(fyne.NewSize(400, 500))
		w.Show()
		p.autoPlayLastTrack()
	} else if p.settings.LastFile != "" {
		if _, err := os.Stat(p.settings.LastFile); err == nil {
			p.loadPlaylist(p.settings.LastFile)
			p.setupUI()
			w.Resize(fyne.NewSize(400, 500))
			w.Show()
			p.autoPlayLastTrack()
		} else {
			p.showFileDialog(w, a)
		}
	} else {
		p.showFileDialog(w, a)
	}

	a.Run()

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
	p.currentTrack = widget.NewLabelWithStyle("Wähle einen Sender", fyne.TextAlignCenter, fyne.TextStyle{Italic: true})
	p.currentTrack.Wrapping = fyne.TextTruncate

	p.volume = widget.NewSlider(0, 100)
	p.volume.SetValue(float64(p.settings.Volume))
	p.volume.Step = 1
	p.volume.OnChanged = func(val float64) {
		v := int(val)
		p.setVolume(v)
		p.settings.Volume = v
		saveSettings(p.settings)
	}

	p.searchEntry = widget.NewEntry()
	p.searchEntry.SetPlaceHolder("Sender suchen...")
	p.searchEntry.OnChanged = p.filterPlaylist

	p.playStopBtn = widget.NewButtonWithIcon("", theme.MediaPlayIcon(), p.playFirstOrStop)

	p.muteBtn = widget.NewButtonWithIcon("", theme.VolumeUpIcon(), p.toggleMute)

	randomBtn := widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), p.playRandom)

	openBtn := widget.NewButtonWithIcon("", theme.FolderOpenIcon(), func() {
		fd := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
			if err != nil || r == nil {
				return
			}
			r.Close()
			if p.loadPlaylist(r.URI().Path()) {
				p.searchEntry.SetText("")
				p.list.Refresh()
			}
		}, p.window)
		fd.SetFilter(storage.NewExtensionFileFilter([]string{".m3u8", ".m3u"}))
		fd.Show()
	})

	var settingsBtn *widget.Button
	settingsBtn = widget.NewButtonWithIcon("", theme.SettingsIcon(), func() {
		installItem := fyne.NewMenuItem("Install to Ubuntu", func() {
			iconData, err := iconFS.ReadFile("icon.png")
			if err != nil {
				dialog.ShowError(fmt.Errorf("could not read icon: %w", err), p.window)
				return
			}
			if installDesktopEntry(iconData) {
				dialog.ShowInformation("Installed", "Radio Player added to Ubuntu applications.", p.window)
			}
		})
		menu := fyne.NewMenu("", installItem)
		popUp := widget.NewPopUpMenu(menu, p.window.Canvas())
		btnPos := fyne.CurrentApp().Driver().AbsolutePositionForObject(settingsBtn)
		popUp.ShowAtPosition(fyne.NewPos(btnPos.X, btnPos.Y+settingsBtn.Size().Height))
	})

	p.list = widget.NewList(
		func() int { return len(p.filteredList) },
		func() fyne.CanvasObject {
			label := widget.NewLabel("Sender Name")
			label.Resize(fyne.NewSize(0, 24))
			return label
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			label := obj.(*widget.Label)
			if p.isPlayingTrack(p.filteredList[id]) {
				label.TextStyle = fyne.TextStyle{Bold: true}
			} else {
				label.TextStyle = fyne.TextStyle{}
			}
			label.SetText(p.filteredList[id].Name)
		},
	)

	p.list.OnSelected = func(id widget.ListItemID) {
		p.playTrack(id)
		p.list.UnselectAll()
	}

	countLabel := widget.NewLabel(fmt.Sprintf("%d Sender", len(p.playlist)))

	controlRow := container.NewBorder(nil, nil, p.muteBtn, container.NewHBox(p.playStopBtn, randomBtn, openBtn, settingsBtn), p.volume)

	controls := container.NewVBox(
		p.currentTrack,
		widget.NewSeparator(),
		controlRow,
		widget.NewSeparator(),
		container.NewBorder(nil, nil, nil, countLabel, p.searchEntry),
		widget.NewSeparator(),
	)

	content := container.NewBorder(controls, nil, nil, nil, p.list)

	p.window.SetContent(content)
}

func (p *Player) playFirstOrStop() {
	if p.playingIdx >= 0 {
		p.stopPlayback()
	} else if len(p.filteredList) > 0 {
		p.playTrack(0)
	}
}

func (p *Player) playRandom() {
	if len(p.filteredList) == 0 {
		return
	}
	rand.Seed(time.Now().UnixNano())
	randomIdx := rand.Intn(len(p.filteredList))
	p.playTrack(randomIdx)
}

func (p *Player) updatePlayStopBtn() {
	if p.playingIdx >= 0 {
		p.playStopBtn.SetIcon(theme.MediaStopIcon())
	} else {
		p.playStopBtn.SetIcon(theme.MediaPlayIcon())
	}
}

func (p *Player) autoPlayLastTrack() {
	if p.settings.LastTrackURL == "" {
		return
	}
	for i, track := range p.playlist {
		if track.URL == p.settings.LastTrackURL {
			p.playTrack(widget.ListItemID(i))
			return
		}
	}
}

func (p *Player) toggleMute() {
	if p.isMuted {
		p.isMuted = false
		p.setVolume(p.savedVolume)
		p.volume.SetValue(float64(p.savedVolume))
		p.muteBtn.SetIcon(theme.VolumeUpIcon())
	} else {
		p.isMuted = true
		p.savedVolume = int(p.volume.Value)
		p.setVolume(0)
		p.muteBtn.SetIcon(theme.VolumeMuteIcon())
	}
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

	// Stop without clearing settings
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
		p.currentTrack.SetText("Fehler beim Laden")
		p.playingIdx = -1
		return
	}

	p.mediaPlayer = C.libvlc_media_player_new_from_media(p.media)
	if p.mediaPlayer == nil {
		p.currentTrack.SetText("Fehler beim Erstellen des Players")
		p.playingIdx = -1
		return
	}

	p.setVolume(int(p.volume.Value))
	C.libvlc_media_player_play(p.mediaPlayer)
	p.currentTrack.SetText(fmt.Sprintf("Spielt: %s", track.Name))
	p.settings.LastTrackURL = track.URL
	saveSettings(p.settings)
	p.updatePlayStopBtn()
	p.list.Refresh()
	p.list.ScrollTo(id)
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
	p.settings.LastTrackURL = ""
	saveSettings(p.settings)
	p.currentTrack.SetText("Gestoppt")
	p.updatePlayStopBtn()
	p.list.Refresh()
}

func (p *Player) loadPlaylist(filename string) bool {
	tracks, err := parseM3U8(filename)
	if err != nil {
		return false
	}
	p.playlist = tracks
	p.filteredList = tracks
	p.currentFile = filename

	// Save absolute path so it works when launched from desktop
	absPath, err := filepath.Abs(filename)
	if err != nil {
		absPath = filename
	}
	p.settings.LastFile = absPath
	saveSettings(p.settings)
	return true
}

func (p *Player) showFileDialog(w fyne.Window, a fyne.App) {
	w.Resize(fyne.NewSize(400, 500))
	w.Show()
	fd := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
		if err != nil || r == nil {
			a.Quit()
			return
		}
		r.Close()
		if !p.loadPlaylist(r.URI().Path()) {
			dialog.ShowError(fmt.Errorf("could not load playlist"), w)
			a.Quit()
			return
		}
		p.setupUI()
	}, w)
	fd.SetFilter(storage.NewExtensionFileFilter([]string{".m3u8", ".m3u"}))
	fd.Show()
}
