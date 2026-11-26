package forward

import (
	"cmp"
	"slices"
	"time"

	"github.com/crmin/kuri/internal/menuview"
	"github.com/crmin/kuri/internal/route"
)

func compareTarget(a, b route.Target) int {
	return cmp.Or(cmp.Compare(a.Context, b.Context), cmp.Compare(a.Namespace, b.Namespace),
		cmp.Compare(a.Service, b.Service), cmp.Compare(a.Port, b.Port))
}

func compareRecent(aTime time.Time, a route.Target, bTime time.Time, b route.Target) int {
	return cmp.Or(bTime.Compare(aTime), compareTarget(a, b))
}

// RecentSnapshot counts the active forwards and copies only the selected rows
// from the same observation. The small candidate window stays ordered.
func (m *Manager) RecentSnapshot(limit int) (int, []Row) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	best := make([]*entry, 0, limit)
	var count int
	add := func(e *entry) {
		count++
		if e.lastAccess.IsZero() {
			return
		}
		at, _ := slices.BinarySearchFunc(best, e, func(a, b *entry) int {
			return compareRecent(a.lastAccess, a.target, b.lastAccess, b.target)
		})
		if at == limit {
			return
		}
		if len(best) < limit {
			best = append(best, nil)
		}
		copy(best[at+1:], best[at:])
		best[at] = e
	}
	for _, e := range m.entries {
		if isActive(e, now) {
			add(e)
		}
	}
	for e := range m.ephemeral {
		if e.lease.isAlive() {
			add(e)
		}
	}
	rows := make([]Row, len(best))
	for i, e := range best {
		rows[i] = m.snapshotRow(e)
	}
	return count, rows
}

func (m *Manager) recordLocked(e *entry) {
	row := m.snapshotRow(e)
	m.history = slices.DeleteFunc(m.history, func(entry Row) bool { return entry.Target == row.Target })
	at, _ := slices.BinarySearchFunc(m.history, row, func(a, b Row) int {
		return compareRecent(*a.LastAccess, a.Target, *b.LastAccess, b.Target)
	})
	if at < menuview.RecentLimit {
		m.history = slices.Insert(m.history, at, row)
		m.history = m.history[:min(len(m.history), menuview.RecentLimit)]
	}
}

func (m *Manager) RecentHistory() (int, []Row) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	now := time.Now()
	for _, e := range m.entries {
		if isActive(e, now) {
			count++
		}
	}
	for e := range m.ephemeral {
		if e.lease.isAlive() {
			count++
		}
	}
	return count, slices.Clone(m.history)
}

// RecentRows selects from an older daemon's complete state reply.
func RecentRows(rows []Row, limit int) ([]Row, bool) {
	var recent []Row
	for _, row := range rows {
		if row.LastAccess == nil {
			return nil, false
		}
		if !row.LastAccess.IsZero() {
			recent = append(recent, row)
		}
	}
	slices.SortFunc(recent, func(a, b Row) int {
		return compareRecent(*a.LastAccess, a.Target, *b.LastAccess, b.Target)
	})
	return recent[:min(limit, len(recent))], true
}
