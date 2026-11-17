package model

import "time"

// SLLCatalogImport records the last completely applied public catalog for one credential group.
type SLLCatalogImport struct {
	ChannelID        string `gorm:"primaryKey;size:36"`
	SupplierGroup    string
	CredentialDigest string
	SourceVersion    string
	CollectedAt      time.Time
	Added            int
	Updated          int
	ExclusionsJSON   string `gorm:"type:text"`
}

// Lines remain addressable when the supplier withdraws a model or the operator disables it.
type SLLCatalogLine struct {
	ChannelID      string `gorm:"primaryKey;size:36"`
	ModelID        string `gorm:"primaryKey;size:120"`
	ChannelModelID string `gorm:"size:36;index"`
	EntryJSON      string `gorm:"type:text"`
	Available      bool
	Reason         string
}

// All exact model IDs in the supplier response share one atomic UTC-hour snapshot.
type SLLMetricHour struct {
	Hour        time.Time `gorm:"primaryKey;autoIncrement:false"`
	AttemptedAt time.Time
	Successful  bool
	MetricsJSON string `gorm:"type:text"`
}
