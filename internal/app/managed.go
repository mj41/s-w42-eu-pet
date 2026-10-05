package app

// The robot's app list, as its owner set it on the Stackchan manager and the manager signed it,
// relayed to the robot (it checks the signature itself; s-w42-eu-raw does the same): when the
// robot connects and when the version changes (every minute; the manager's answers are cached).

import (
	"context"
	"encoding/base64"
	"encoding/json"

	"github.com/mj41/s-w42-eu-raw/robotauth"
	"github.com/mj41/s-w42-eu-raw/wire"
)

func (a *App) relayManaged(ctx context.Context, c *robotConn) {
	if a.cfg.Manager == nil || c.mgrToken == "" {
		return
	}
	versions, _ := c.appsVersions.Load().(string)
	auth, err := a.cfg.Manager.Seen(ctx, c.id, c.mgrToken, robotauth.Seen{Firmware: c.firmware, AppsVersions: versions})
	if err != nil || !auth.OK || auth.Managed == nil {
		return
	}
	b, err := base64.StdEncoding.DecodeString(auth.Managed.Payload)
	if err != nil {
		return
	}
	var p struct {
		Version int32 `json:"version"`
	}
	json.Unmarshal(b, &p)
	for {
		sent := c.managedSent.Load()
		if p.Version <= sent {
			return
		}
		if c.managedSent.CompareAndSwap(sent, p.Version) {
			break
		}
	}
	c.frame(wire.KindManagedApps, wire.ManagedAppsBody{Payload: auth.Managed.Payload, Sig: auth.Managed.Sig})
	a.log.Info("app list relayed", "robot", c.id, "version", p.Version)
}

func (a *App) relayAllManaged(ctx context.Context) {
	a.mu.Lock()
	var conns []*robotConn
	for _, rb := range a.robots {
		if rb.conn != nil && rb.conn.mgrToken != "" {
			conns = append(conns, rb.conn)
		}
	}
	a.mu.Unlock()
	for _, c := range conns {
		a.relayManaged(ctx, c)
	}
}
