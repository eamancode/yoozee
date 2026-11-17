package sllcatalog

const (
	PricingURL = "https://api.lecapi.net/api/pricing"
	MediaURL   = "https://api.lecapi.net/api/media-models/catalog"
	StatusURL  = "https://api.lecapi.net/api/status"
	MetricsURL = "https://api.lecapi.net/api/perf-metrics/public"
)

type Catalog struct {
	Version  string      `json:"version"`
	Entries  []Entry     `json:"entries"`
	Excluded []Exclusion `json:"excluded"`
}

type Entry struct {
	ModelID      string       `json:"modelId"`
	DisplayName  string       `json:"displayName"`
	Description  string       `json:"description"`
	Group        string       `json:"group"`
	GroupRatio   float64      `json:"groupRatio"`
	Tiers        []Tier       `json:"tiers"`
	Capabilities Capabilities `json:"capabilities"`
}

type Tier struct {
	Resolution   string  `json:"resolution"`
	PriceCNY     float64 `json:"priceCny"`
	CostMicroCNY int64   `json:"costMicroCny"`
}

type Exclusion struct {
	ModelID    string `json:"modelId"`
	Resolution string `json:"resolution,omitempty"`
	Reason     string `json:"reason"`
}

type Capabilities struct {
	DurationMin     int      `json:"durationMin"`
	DurationMax     int      `json:"durationMax"`
	DurationDefault int      `json:"durationDefault"`
	Durations       []int    `json:"durations"`
	AspectRatios    []string `json:"aspectRatios"`
	MinImages       int      `json:"minImages"`
	MaxImages       *int     `json:"maxImages"`
	MaxVideos       *int     `json:"maxVideos"`
	MaxAudios       *int     `json:"maxAudios"`
	MaxReferences   *int     `json:"maxReferences"`
	MaxVideoSeconds *int     `json:"maxVideoSeconds"`
	GenerateAudio   *bool    `json:"generateAudio"`
	SendDuration    bool     `json:"sendDuration"`
	SendResolution  bool     `json:"sendResolution"`
	FixedResolution string   `json:"fixedResolution,omitempty"`
	MaxPromptChars  int      `json:"maxPromptChars,omitempty"`
	MaxImageBytes   int64    `json:"maxImageBytes,omitempty"`
	MaxVideoBytes   int64    `json:"maxVideoBytes,omitempty"`
	MaxAudioBytes   int64    `json:"maxAudioBytes,omitempty"`
	ImageMIMEs      []string `json:"imageMimes,omitempty"`
	VideoMIMEs      []string `json:"videoMimes,omitempty"`
	AudioMIMEs      []string `json:"audioMimes,omitempty"`
}

type Metric struct {
	Rate        *float64 `json:"rate"`
	SampleCount *int64   `json:"sampleCount"`
}
