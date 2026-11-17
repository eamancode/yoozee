package model

import "time"

// Model is the public picker value. Upstream keys may differ between price tiers.
type ProjectVideoRoute struct {
	ProjectID   string     `gorm:"primaryKey;size:36" json:"-"`
	Model       string     `gorm:"size:500" json:"model"`
	FromModel   string     `gorm:"size:500" json:"fromModel"`
	Status      string     `gorm:"size:24" json:"status"`
	Reason      string     `json:"reason"`
	SuccessRate *float64   `json:"successRate"`
	SampledAt   *time.Time `json:"sampledAt"`
	ChangedAt   time.Time  `json:"changedAt"`
	Revision    int64      `json:"revision"`
}

type ProjectVideoRouteStreak struct {
	ProjectID        string `gorm:"primaryKey;size:36"`
	ChannelID        string `gorm:"primaryKey;size:36"`
	ProviderModelKey string `gorm:"primaryKey;size:240"`
	Failures         int
}

// Reclaiming a worker lease leaves RouteRun unchanged.
type ProjectVideoRouteEvent struct {
	TaskID           string `gorm:"primaryKey;size:36"`
	RouteRun         int    `gorm:"primaryKey;autoIncrement:false"`
	ProjectID        string `gorm:"size:36;index"`
	ChannelID        string `gorm:"size:36"`
	ProviderModelKey string `gorm:"size:240"`
	Decision         string `gorm:"size:24"`
	CreatedAt        time.Time
}
