package app

import (
	"encoding/json"
	"hash/crc32"
	"slices"
	"sort"
	"time"

	"github.com/mj41/stackchan-pet/internal/robotpic"
	"github.com/mj41/stackchan-server/wire"
)

// The pet keeps its pictures in the robot's file store (folder "pet/"), so it
// can later show them without sending them again. At connect it asks for the
// robot's file list ("assets") and uploads what is missing or changed (CRC-32).

const assetDir = "pet/"

// petAssets are the files the pet wants on the robot.
func petAssets() map[string][]byte {
	out := map[string][]byte{}
	for name, b := range robotpic.PNGs() {
		out[assetDir+name] = b
	}
	return out
}

// syncAssets compares the robot's list with the pet's files and uploads the difference. a.mu held.
func (a *App) syncAssets(r *robot, listJSON string) {
	var list struct {
		Files []struct {
			Name  string `json:"name"`
			Bytes int    `json:"bytes"`
			CRC   uint32 `json:"crc"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(listJSON), &list); err != nil || r.conn == nil {
		return
	}
	have := map[string]uint32{}
	for _, f := range list.Files {
		have[f.Name] = f.CRC
	}
	want := petAssets()
	for name, b := range want {
		if have[name] == crc32.ChecksumIEEE(b) {
			r.files[name] = true
		}
	}
	names := make([]string, 0, len(want))
	for name, b := range want {
		if crc, ok := have[name]; !ok || crc != crc32.ChecksumIEEE(b) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		a.log.Info("robot files up to date", "robot", r.id, "files", len(want))
		return
	}
	r.uploading = map[string]uint32{}
	var chunks [][]byte
	for _, name := range names {
		r.uploading[name] = crc32.ChecksumIEEE(want[name])
		chunks = append(chunks, wire.AssetChunks(name, want[name])...)
	}
	a.log.Info("uploading pet files", "robot", r.id, "files", len(names), "chunks", len(chunks))
	go a.sendChunks(r.conn, chunks)
}

// sendChunks queues upload messages without crowding out commands.
func (a *App) sendChunks(c *robotConn, chunks [][]byte) {
	for _, msg := range chunks {
		for len(c.send) > cap(c.send)/2 {
			select {
			case <-c.done:
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
		if !c.queue(outMsg{binary: true, data: msg}) {
			return
		}
	}
}

// assetSaved checks an uploaded file's CRC. a.mu held.
func (a *App) assetSaved(r *robot, name string, crc uint32) {
	want, ok := r.uploading[name]
	if !ok {
		return
	}
	delete(r.uploading, name)
	if crc != want {
		a.log.Warn("robot file differs after upload", "robot", r.id, "name", name, "crc", crc, "want", want)
		return
	}
	r.files[name] = true
	if len(r.uploading) == 0 {
		a.log.Info("pet files uploaded", "robot", r.id)
	}
}

// canSprite: the robot shows stored pictures and has this one.
func (r *robot) canSprite(asset string) bool {
	return r.files[asset] && slices.Contains(r.commands, "sprite")
}

// sprite sends a "sprite" command (see the firmware's SpriteLayer).
func (a *App) sprite(r *robot, args map[string]any) {
	r.spritesOn = true
	r.conn.command("sprite", args)
}
