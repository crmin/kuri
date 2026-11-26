package menuview

import "time"

const RecentLimit = 5

type State struct {
	Mode            string     `json:"mode"`
	PID             int        `json:"pid"`
	Started         *time.Time `json:"started,omitempty"`
	Connected       int        `json:"connected"`
	RecentAvailable bool       `json:"recent_available"`
	Rows            []Forward  `json:"rows"`
}

type Forward struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	URL   string `json:"url,omitempty"`
}
