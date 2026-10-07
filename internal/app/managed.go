package app

// The browsers paired with a robot set up by a Stackchan manager, reported to the manager every
// 15 s (robot-auth "seen"); the answer names the pairings its owner removed there. The robot's apps
// are its manager's business: the robot talks to it on its own channel.

import (
	"context"

	"github.com/mj41/s-w42-eu-raw/robotauth"
)

func (a *App) reportSeen(ctx context.Context, c *robotConn) {
	if a.cfg.Manager == nil || c.mgrToken == "" {
		return
	}
	a.mu.Lock()
	pairings := a.pairings(c.id)
	a.mu.Unlock()
	auth, err := a.cfg.Manager.Seen(ctx, c.id, c.mgrToken, robotauth.Seen{Pairings: pairings})
	if err != nil || !auth.OK {
		return
	}
	a.mu.Lock() // a new owner (two users joined at the manager): now, not at the robot's next connect
	if rb := a.robots[c.id]; rb != nil && auth.Owner != "" && rb.owner != auth.Owner {
		a.setOwnerLocked(c.id, auth.Owner)
	}
	a.mu.Unlock()
	a.unpair(c.id, auth.Unpair)
}

func (a *App) reportAllSeen(ctx context.Context) {
	a.mu.Lock()
	var conns []*robotConn
	for _, rb := range a.robots {
		if rb.conn != nil && rb.conn.mgrToken != "" {
			conns = append(conns, rb.conn)
		}
	}
	a.mu.Unlock()
	for _, c := range conns {
		a.reportSeen(ctx, c)
	}
}
