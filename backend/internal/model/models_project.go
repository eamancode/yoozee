package model

import (
	"html"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var projectUnitHTMLTagPattern = regexp.MustCompile(`<[^>]+>`)

const ProjectUnitTitleMaxRunes = 240

func ProjectUnitWordCount(sourceText string) int {
	plainText := projectUnitHTMLTagPattern.ReplaceAllString(sourceText, "")
	return utf8.RuneCountInString(strings.TrimSpace(html.UnescapeString(plainText)))
}

func NormalizeProjectUnitTitle(value string) string {
	return strings.TrimSpace(value)
}

func ValidProjectUnitTitle(value string) bool {
	title := NormalizeProjectUnitTitle(value)
	return title != "" && utf8.RuneCountInString(title) <= ProjectUnitTitleMaxRunes
}

const AssetIDMaxLength = 80

type Resource struct {
	ID       string         `json:"id" gorm:"primaryKey;size:36"`
	UserID   string         `json:"userId" gorm:"index;size:36;index:idx_resources_user_created,priority:1;uniqueIndex:idx_resources_user_upload_key,priority:1"`
	Kind     string         `json:"kind" gorm:"index;size:24"`
	Status   ResourceStatus `json:"status" gorm:"index;size:24"`
	Provider string         `json:"provider" gorm:"size:24"`
	Endpoint string         `json:"endpoint"`
	Bucket   string         `json:"bucket" gorm:"size:160"`
	// 用户 OSS 每次修改都会生成新版本，资源固定引用创建时的存储与密钥；只有同一存储位置才可复用当前 CDN。
	StorageSettingID string `json:"-" gorm:"index;size:36"`
	ObjectKey        string `json:"objectKey" gorm:"index"`
	PublicURL        string `json:"publicUrl"`
	MimeType         string `json:"mimeType" gorm:"size:120"`
	Size             int64  `json:"size"`
	Width            int    `json:"width"`
	Height           int    `json:"height"`
	DurationMs       int64  `json:"durationMs"`
	ETag             string `json:"etag" gorm:"size:160"`
	// 浏览器兼容播放副本（HEVC/H.265 原片在 Chrome 等无法解码，由 ffmpeg 转 H.264）：
	// PlaybackStatus: none|processing|ready|failed；PlaybackObjectKey 为本地播放目录下的文件名。
	PlaybackStatus    string `json:"playbackStatus" gorm:"index;size:24"`
	PlaybackObjectKey string `json:"playbackObjectKey"`
	PlaybackError     string `json:"playbackError" gorm:"type:text"`
	// 单档画布展示缩略图；对象键固定由 ObjectKey 派生，不单独持久化。
	ThumbnailStatus   string `json:"thumbnailStatus" gorm:"size:24"`
	ThumbnailMimeType string `json:"thumbnailMimeType" gorm:"size:120"`
	ThumbnailSize     int64  `json:"thumbnailSize"`
	ThumbnailWidth    int    `json:"thumbnailWidth"`
	ThumbnailHeight   int    `json:"thumbnailHeight"`
	// UploadKey 是客户端逻辑上传身份的摘要；NULL 表示不参与幂等约束。
	UploadKey *string   `json:"-" gorm:"size:64;uniqueIndex:idx_resources_user_upload_key,priority:2"`
	Error     string    `json:"error"`
	CreatedAt time.Time `json:"createdAt" gorm:"index:idx_resources_user_created,priority:2"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ResourceDeletionJob is the durable handoff between database deletion and
// physical object cleanup. Storage fields are frozen because the Resource row
// is removed in the same transaction that creates this job.
type ResourceDeletionJob struct {
	ID               string                 `json:"id" gorm:"primaryKey;size:36"`
	UserID           string                 `json:"userId" gorm:"index;size:36"`
	ResourceID       string                 `json:"resourceId" gorm:"index;size:36"`
	Provider         string                 `json:"provider" gorm:"size:24"`
	Endpoint         string                 `json:"endpoint"`
	Bucket           string                 `json:"bucket" gorm:"size:160"`
	StorageSettingID string                 `json:"-" gorm:"index;size:36"`
	ObjectKey        string                 `json:"objectKey" gorm:"index"`
	Status           ResourceDeletionStatus `json:"status" gorm:"index:idx_resource_deletion_jobs_due,priority:1;size:24"`
	Attempts         int                    `json:"attempts"`
	LastError        string                 `json:"lastError" gorm:"type:text"`
	NextAttemptAt    time.Time              `json:"nextAttemptAt" gorm:"index:idx_resource_deletion_jobs_due,priority:2"`
	LeaseOwner       string                 `json:"-" gorm:"index;size:120"`
	LeaseExpiresAt   *time.Time             `json:"-" gorm:"index"`
	CreatedAt        time.Time              `json:"createdAt"`
	UpdatedAt        time.Time              `json:"updatedAt"`
}

// AnnouncementImageDraft marks an uploaded image as temporary until an
// announcement create or update transaction consumes it.
type AnnouncementImageDraft struct {
	ResourceID string    `json:"resourceId" gorm:"primaryKey;size:36"`
	UserID     string    `json:"userId" gorm:"index;size:36"`
	CreatedAt  time.Time `json:"createdAt" gorm:"index"`
}

type Asset struct {
	ID               string             `json:"id" gorm:"primaryKey;size:80"`
	UserID           string             `json:"userId" gorm:"index;size:36;index:idx_assets_user_updated,priority:1"`
	FolderID         string             `json:"folderId,omitempty" gorm:"index;size:36"`
	Kind             string             `json:"kind" gorm:"index;size:24"`
	Category         AssetCategory      `json:"category" gorm:"index;size:32"`
	Status           AssetVersionStatus `json:"status" gorm:"index;size:24"`
	PrimaryVersionID string             `json:"primaryVersionId,omitempty" gorm:"index;size:36"`
	Title            string             `json:"title" gorm:"size:240"`
	PayloadJSON      string             `json:"payloadJson" gorm:"type:text"`
	// ParentAssetID / VariantLabel 是角色子卡（M8 卡 D）：子卡是独立的角色资产，指向主卡并记下变体名（「护士服」「淋湿」）；主卡和别的资产为空。
	ParentAssetID string    `json:"parentAssetId,omitempty" gorm:"index;size:80;default:''"`
	VariantLabel  string    `json:"variantLabel,omitempty" gorm:"size:80;default:''"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt" gorm:"index:idx_assets_user_updated,priority:2"`
}

// AssetFolder 是用户素材库的一层自定义分类；业务分类仍由 Asset.Category 表达。
type AssetFolder struct {
	ID        string    `json:"id" gorm:"primaryKey;size:36"`
	UserID    string    `json:"userId" gorm:"index;size:36;uniqueIndex:idx_asset_folders_user_name,priority:1"`
	Name      string    `json:"name" gorm:"size:80"`
	NameKey   string    `json:"-" gorm:"size:80;uniqueIndex:idx_asset_folders_user_name,priority:2"`
	Position  int       `json:"position" gorm:"index"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type ProjectAssetLink struct {
	ID        string    `json:"id" gorm:"primaryKey;size:36"`
	ProjectID string    `json:"projectId" gorm:"index;size:36;uniqueIndex:idx_project_asset_links_unique,priority:1;index:idx_project_asset_links_project_folder_position,priority:1"`
	AssetID   string    `json:"assetId" gorm:"index;size:80;uniqueIndex:idx_project_asset_links_unique,priority:2"`
	FolderID  string    `json:"folderId,omitempty" gorm:"index;size:36;index:idx_project_asset_links_project_folder_position,priority:2"`
	Position  int       `json:"position" gorm:"index;index:idx_project_asset_links_project_folder_position,priority:3"`
	CreatedAt time.Time `json:"createdAt"`
}

// ProjectAssetFolder 只保存项目内的目录结构；真实媒体仍由 Asset/Resource 唯一持有。
type ProjectAssetFolder struct {
	ID        string    `json:"id" gorm:"primaryKey;size:36"`
	ProjectID string    `json:"projectId" gorm:"index;size:36;uniqueIndex:idx_project_asset_folders_sibling_name,priority:1"`
	ParentID  string    `json:"parentId,omitempty" gorm:"index;size:36;uniqueIndex:idx_project_asset_folders_sibling_name,priority:2"`
	Name      string    `json:"name" gorm:"size:240"`
	NameKey   string    `json:"-" gorm:"size:240;uniqueIndex:idx_project_asset_folders_sibling_name,priority:3"`
	Style     string    `json:"style" gorm:"size:24"`
	Theme     string    `json:"theme" gorm:"size:24"`
	Position  int       `json:"position" gorm:"index"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type ProjectAssetCandidate struct {
	ID              string        `json:"id" gorm:"primaryKey;size:36"`
	ProjectID       string        `json:"projectId" gorm:"index;size:36;index:idx_project_asset_candidates_project_unit_status,priority:1;index:idx_project_asset_candidates_project_status_category,priority:1"`
	UnitID          string        `json:"unitId,omitempty" gorm:"index;size:36;index:idx_project_asset_candidates_project_unit_status,priority:2"`
	ShotID          string        `json:"shotId,omitempty" gorm:"index;size:36"`
	Name            string        `json:"name" gorm:"size:240"`
	NameKey         string        `json:"-" gorm:"index;size:240"`
	Category        AssetCategory `json:"category" gorm:"index;size:32;index:idx_project_asset_candidates_project_status_category,priority:3"`
	Status          string        `json:"status" gorm:"index;size:32;index:idx_project_asset_candidates_project_unit_status,priority:3;index:idx_project_asset_candidates_project_status_category,priority:2"`
	Source          string        `json:"source,omitempty" gorm:"index;size:48"`
	DetailsJSON     string        `json:"detailsJson" gorm:"type:text"`
	ResolvedAssetID string        `json:"resolvedAssetId,omitempty" gorm:"index;size:80"`
	CreatedAt       time.Time     `json:"createdAt"`
	UpdatedAt       time.Time     `json:"updatedAt"`
}

type AssetVersion struct {
	ID             string             `json:"id" gorm:"primaryKey;size:36"`
	AssetID        string             `json:"assetId" gorm:"index;size:80;uniqueIndex:idx_asset_versions_number,priority:1"`
	Version        int                `json:"version" gorm:"uniqueIndex:idx_asset_versions_number,priority:2"`
	Status         AssetVersionStatus `json:"status" gorm:"index;size:24"`
	DefinitionJSON string             `json:"definitionJson" gorm:"type:text"`
	Prompt         string             `json:"prompt" gorm:"type:text"`
	Note           string             `json:"note" gorm:"size:500"`
	CreatedAt      time.Time          `json:"createdAt"`
	UpdatedAt      time.Time          `json:"updatedAt"`
}

type AssetRepresentation struct {
	ID             string    `json:"id" gorm:"primaryKey;size:36"`
	TaskID         string    `json:"taskId,omitempty" gorm:"index;size:36;uniqueIndex:idx_asset_representations_task_role,priority:1"`
	AssetVersionID string    `json:"assetVersionId" gorm:"index;size:36;uniqueIndex:idx_asset_representations_version_role,priority:1"`
	ResourceID     string    `json:"resourceId,omitempty" gorm:"index;size:36"`
	MediaType      string    `json:"mediaType" gorm:"index;size:24"`
	Role           string    `json:"role" gorm:"index;size:32;uniqueIndex:idx_asset_representations_task_role,priority:2;uniqueIndex:idx_asset_representations_version_role,priority:2"`
	MetadataJSON   string    `json:"metadataJson" gorm:"type:text"`
	CreatedAt      time.Time `json:"createdAt"`
}

// VoiceProfile 是可复用的声音身份；试听音频只是表现资源，不等同于声音本身。
type VoiceProfile struct {
	ID                   string    `json:"id" gorm:"primaryKey;size:36"`
	UserID               string    `json:"userId" gorm:"index;size:36;uniqueIndex:idx_voice_profiles_user_provider_key,priority:1"`
	Name                 string    `json:"name" gorm:"size:160"`
	Provider             string    `json:"provider" gorm:"size:48;uniqueIndex:idx_voice_profiles_user_provider_key,priority:2"`
	VoiceKey             string    `json:"voiceKey" gorm:"size:160;uniqueIndex:idx_voice_profiles_user_provider_key,priority:3"`
	Language             string    `json:"language" gorm:"size:80"`
	Timbre               string    `json:"timbre" gorm:"size:240"`
	SampleResourceID     string    `json:"sampleResourceId,omitempty" gorm:"index;size:36"`
	CompatibleModelsJSON string    `json:"compatibleModelsJson" gorm:"type:text"`
	Status               string    `json:"status" gorm:"index;size:24"`
	CreatedAt            time.Time `json:"createdAt"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

type CharacterVoiceBinding struct {
	ID             string    `json:"id" gorm:"primaryKey;size:36"`
	AssetVersionID string    `json:"assetVersionId" gorm:"uniqueIndex;size:36"`
	VoiceProfileID string    `json:"voiceProfileId" gorm:"index;size:36"`
	Instructions   string    `json:"instructions" gorm:"type:text"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// Project 是短剧领域聚合根；CanvasProject 仍代表可独立创作的画布文档。
type Project struct {
	ID                string `json:"id" gorm:"primaryKey;size:36"`
	UserID            string `json:"userId" gorm:"index;size:36;uniqueIndex:idx_projects_user_name,priority:1"`
	Name              string `json:"name" gorm:"size:240;uniqueIndex:idx_projects_user_name,priority:2"`
	Type              string `json:"type" gorm:"size:32;index"`
	AspectRatio       string `json:"aspectRatio" gorm:"size:16"`
	SourceType        string `json:"sourceType" gorm:"size:32"`
	Description       string `json:"description" gorm:"type:text"`
	CoverResourceID   string `json:"coverResourceId,omitempty" gorm:"index;size:36"`
	StylePresetID     string `json:"stylePresetId" gorm:"size:64"`
	StyleProfileJSON  string `json:"styleProfileJson" gorm:"type:text"`
	DefaultImageModel string `json:"defaultImageModel,omitempty" gorm:"size:500"`
	DefaultVideoModel string `json:"defaultVideoModel,omitempty" gorm:"size:500"`
	// GenerationModel 是建项目时选定的视频生成模型族（kb-dialects/models.json 的 id），决定一段最长几秒。
	GenerationModel string `json:"generationModel" gorm:"size:40"`
	// VisualType 是画面类型：live_action / cg_real / anime_3d / anime_2d。建项必选，之后不可改；平台风格库按它归档。
	VisualType string `json:"visualType" gorm:"size:24"`
	// RunMode 是生成模式：until_video 跑到出视频前；review_from_assets 资产我要过目；every_step 每步都问我。
	// 一步做完后服务端按它决定要不要自动接下一步，出视频在任何模式下都要人点（drama.ShouldAutoContinue）。
	RunMode string `json:"runMode" gorm:"size:24"`
	// StyleSource 是建项时选的画风来源：交给导演分析（可带一句描述和参考图）或从风格中心挑一条。导演定画风那一步按它跑。
	StyleSource ProjectStyleSource `json:"style" gorm:"column:style_source;serializer:json;type:text"`
	// StyleProfile 是 yoozee 风格档案（live-realism、anime-3d …，五选一，与 StyleProfileJSON 的画风快照不是一回事）：
	// 检查、装段用它的配额与规则，短剧流程每一步按它查 drama_step_bindings 取技能。
	StyleProfile string `json:"styleProfile" gorm:"size:32"`
	// BriefJSON / BibleJSON 是需求文档与连戏表（契约 brief、bible），由短剧技能读写。
	BriefJSON string        `json:"briefJson" gorm:"type:text"`
	BibleJSON string        `json:"bibleJson" gorm:"type:text"`
	Status    ProjectStatus `json:"status" gorm:"index;size:24"`
	Revision  int64         `json:"revision"`
	CreatedAt time.Time     `json:"createdAt"`
	UpdatedAt time.Time     `json:"updatedAt" gorm:"index"`
}

// 画风来源（projects.style_source 的 mode）。
const (
	ProjectStyleDirector = "director"
	ProjectStyleLibrary  = "library"
)

// ProjectStyleSource 是项目的画风来源：mode=director 时 Brief / RefImageIDs 是导演分析的输入（都可以不填，导演按故事自己定）；
// mode=library 时 StyleID 是平台风格库的条目（建项时直接锁定，不跑风格分析）。
type ProjectStyleSource struct {
	Mode        string   `json:"mode"`
	Brief       string   `json:"brief,omitempty"`
	RefImageIDs []string `json:"refImageIds,omitempty"`
	StyleID     string   `json:"styleId,omitempty"`
}

// StyleProfile 是用户可持续编辑的风格源；项目只保存应用当时的 JSON 快照，避免源对象更新污染历史生成。
type StyleProfile struct {
	ID          string     `json:"id" gorm:"primaryKey;size:36"`
	UserID      string     `json:"userId" gorm:"index;size:36;index:idx_style_profiles_user_updated,priority:1"`
	Name        string     `json:"name" gorm:"size:160"`
	Description string     `json:"description" gorm:"size:500"`
	CoverURL    string     `json:"coverUrl" gorm:"type:text"`
	TagsJSON    string     `json:"tagsJson" gorm:"type:text"`
	ProfileJSON string     `json:"profileJson" gorm:"type:text"`
	Favorite    bool       `json:"favorite" gorm:"index"`
	LastUsedAt  *time.Time `json:"lastUsedAt,omitempty" gorm:"index"`
	Revision    int64      `json:"revision"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt" gorm:"index:idx_style_profiles_user_updated,priority:2"`
}

type ProjectUnit struct {
	ID         string            `json:"id" gorm:"primaryKey;size:36"`
	ProjectID  string            `json:"projectId" gorm:"index;size:36;index:idx_project_units_project_position,priority:1"`
	ParentID   string            `json:"parentId,omitempty" gorm:"index;size:36"`
	Kind       ProjectUnitKind   `json:"kind" gorm:"index;size:24"`
	Title      string            `json:"title" gorm:"size:240"`
	SourceText string            `json:"sourceText" gorm:"type:text"`
	WordCount  int               `json:"wordCount"`
	Status     ProjectUnitStatus `json:"status" gorm:"index;size:24"`
	Position   int               `json:"position" gorm:"index:idx_project_units_project_position,priority:2"`
	CreatedAt  time.Time         `json:"createdAt"`
	UpdatedAt  time.Time         `json:"updatedAt"`
}

type CanvasUnitLink struct {
	ID        string    `json:"id" gorm:"primaryKey;size:36"`
	ProjectID string    `json:"projectId" gorm:"index;size:36;uniqueIndex:idx_canvas_unit_links_unique,priority:1;index:idx_canvas_unit_links_project_unit,priority:1"`
	CanvasID  string    `json:"canvasId" gorm:"index;size:80;uniqueIndex:idx_canvas_unit_links_unique,priority:2"`
	UnitID    string    `json:"unitId" gorm:"index;size:36;uniqueIndex:idx_canvas_unit_links_unique,priority:3;index:idx_canvas_unit_links_project_unit,priority:2"`
	Role      string    `json:"role" gorm:"size:32"`
	CreatedAt time.Time `json:"createdAt"`
}

type Shot struct {
	ID                string    `json:"id" gorm:"primaryKey;size:36"`
	ProjectID         string    `json:"projectId" gorm:"index;size:36;index:idx_shots_project_unit_position,priority:1"`
	UnitID            string    `json:"unitId" gorm:"index;size:36;index:idx_shots_project_unit_position,priority:2"`
	CurrentRevisionID string    `json:"currentRevisionId,omitempty" gorm:"index;size:36"`
	Title             string    `json:"title" gorm:"size:240"`
	Description       string    `json:"description" gorm:"type:text"`
	Position          int       `json:"position" gorm:"index:idx_shots_project_unit_position,priority:3"`
	DurationMs        int64     `json:"durationMs"`
	Status            string    `json:"status" gorm:"index;size:24"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

// ShotRevision 保存可复现的分镜脚本版本；Shot 只保留稳定身份、排序和当前版本指针。
type ShotRevision struct {
	ID              string `json:"id" gorm:"primaryKey;size:36"`
	ShotID          string `json:"shotId" gorm:"index;size:36;uniqueIndex:idx_shot_revisions_version,priority:1"`
	Version         int    `json:"version" gorm:"uniqueIndex:idx_shot_revisions_version,priority:2"`
	PlotDescription string `json:"plotDescription" gorm:"type:text"`
	Action          string `json:"action" gorm:"type:text"`
	Dialogue        string `json:"dialogue" gorm:"type:text"`
	ShotSize        string `json:"shotSize" gorm:"size:80"`
	CameraAngle     string `json:"cameraAngle" gorm:"size:80"`
	CameraMovement  string `json:"cameraMovement" gorm:"size:120"`
	DurationMs      int64  `json:"durationMs"`
	ImagePrompt     string `json:"imagePrompt" gorm:"type:text"`
	VideoPrompt     string `json:"videoPrompt" gorm:"type:text"`
	NegativePrompt  string `json:"negativePrompt" gorm:"type:text"`
	ContinuityNotes string `json:"continuityNotes" gorm:"type:text"`
	ActionBeatsJSON string `json:"actionBeatsJson" gorm:"type:text"`
	// SpecJSON 是这一段的中性分段（契约 segment）：段内多镜头、台词、参考和声音；旧镜头为空。
	SpecJSON  string    `json:"specJson,omitempty" gorm:"type:text"`
	CreatedBy string    `json:"createdBy,omitempty" gorm:"index;size:36"`
	CreatedAt time.Time `json:"createdAt"`
}

// ShotArtifact 是镜头的版本化生产产物。修改分镜或资产引用时只标记 stale，不删除历史。
type ShotArtifact struct {
	ID           string `json:"id" gorm:"primaryKey;size:36"`
	ProjectID    string `json:"projectId" gorm:"index;size:36;index:idx_shot_artifacts_project_unit,priority:1"`
	UnitID       string `json:"unitId" gorm:"index;size:36;index:idx_shot_artifacts_project_unit,priority:2"`
	ShotID       string `json:"shotId" gorm:"index;size:36;uniqueIndex:idx_shot_artifacts_version,priority:1"`
	RevisionID   string `json:"revisionId,omitempty" gorm:"index;size:36"`
	TaskID       string `json:"taskId,omitempty" gorm:"index;size:36"`
	Type         string `json:"type" gorm:"index;size:40;uniqueIndex:idx_shot_artifacts_version,priority:2"`
	Version      int    `json:"version" gorm:"uniqueIndex:idx_shot_artifacts_version,priority:3"`
	ResourceID   string `json:"resourceId,omitempty" gorm:"index;size:36"`
	Status       string `json:"status" gorm:"index;size:24"`
	Selected     bool   `json:"selected" gorm:"index"`
	MetadataJSON string `json:"metadataJson" gorm:"type:text"`
	// InputHash 是生成它时依赖输入的指纹；保存新版本时只有指纹变了的产物才过期。旧产物为空，一律按过期处理。
	InputHash string    `json:"inputHash,omitempty" gorm:"size:64"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ShotEvidence 是一把尺子对一条候选（或成片）量出来的结果（契约 evidence）。进度由证据推导，不由人勾选。
// ShotID 为空表示成片级证据（门禁、交付、甲方验收）。
type ShotEvidence struct {
	ID          string    `json:"id" gorm:"primaryKey;size:36"`
	ProjectID   string    `json:"projectId" gorm:"index;size:36;index:idx_shot_evidence_project_artifact,priority:1"`
	UnitID      string    `json:"unitId,omitempty" gorm:"index;size:36"`
	ShotID      string    `json:"shotId,omitempty" gorm:"index;size:36"`
	ArtifactID  string    `json:"artifactId,omitempty" gorm:"index;size:36;index:idx_shot_evidence_project_artifact,priority:2"`
	Kind        string    `json:"kind" gorm:"index;size:24"`
	Passed      *bool     `json:"passed"`
	MetricsJSON string    `json:"metricsJson" gorm:"type:text"`
	ResourceID  string    `json:"resourceId,omitempty" gorm:"index;size:36"`
	TaskID      string    `json:"taskId,omitempty" gorm:"index;size:36"`
	Note        string    `json:"note" gorm:"type:text"`
	CreatedBy   string    `json:"createdBy,omitempty" gorm:"size:36"`
	CreatedAt   time.Time `json:"createdAt" gorm:"index"`
}

// DramaShotlist 是一章的镜头表（yoozee 契约 shotlist）：段由它装出来，用户改镜头表就重装段。
// ScriptJSON 是由章节正文转出的台词表，CheckJSON 是最近一次检查结果。一章一份，按 unit_id 唯一。
type DramaShotlist struct {
	ID           string `json:"id" gorm:"primaryKey;size:36"`
	ProjectID    string `json:"projectId" gorm:"index;size:36;not null"`
	UnitID       string `json:"unitId" gorm:"uniqueIndex;size:36;not null"`
	Version      int    `json:"version" gorm:"not null;default:1"`
	ShotlistJSON string `json:"shotlistJson" gorm:"type:text"`
	ScriptJSON   string `json:"scriptJson" gorm:"type:text"`
	CheckJSON    string `json:"checkJson" gorm:"type:text"`
	// RowsJSON 是这次写入的分镜行原文（评测重放用）；CompileProfileVersion 是装段用的编译档案版本，0 为内置默认。
	RowsJSON string `json:"-" gorm:"type:text"`
	// SectionsJSON 是按小节生成时每一节的原样输出（drama.SectionOutput 数组，M8）：分镜卡按戏类版式显示、只重写几节时作对话历史；
	// 旧镜头表、手写镜头表为空。
	SectionsJSON          string    `json:"-" gorm:"type:text"`
	CompileProfileVersion int       `json:"compileProfileVersion" gorm:"not null;default:0"`
	CreatedBy             string    `json:"createdBy,omitempty" gorm:"size:36"`
	CreatedAt             time.Time `json:"createdAt"`
	UpdatedAt             time.Time `json:"updatedAt"`
}

// DramaCompileProfile 是编译档案的一个版本（M5）：装段、编译里影响质量的文字。content_json 只写要改的项，
// 读取时叠在内置默认（版本 0）上；同一时刻最多一个启用，没有启用的就用内置默认。
type DramaCompileProfile struct {
	ID          string    `json:"id" gorm:"primaryKey;size:36"`
	Version     int       `json:"version" gorm:"uniqueIndex;not null"`
	Name        string    `json:"name" gorm:"size:120"`
	ContentJSON string    `json:"-" gorm:"type:text"`
	Note        string    `json:"note" gorm:"size:500"`
	Enabled     bool      `json:"enabled" gorm:"index"`
	CreatedBy   string    `json:"createdBy" gorm:"size:36"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// DramaEvalRun 是一次评测：M5 的「视频提示词」阶段（stage=compile）选编译档案的候选与基线版本，复用已存的上游产物重跑装段和编译；
// M6 的流程步骤（stage 为步骤 ID，如 chapter.shots）在评测集上用文本模型真跑技能包的候选版本，按步骤门槛决定是否自动启用。
// result_json 是指标表（逐章结果）与逐段提示词；步骤评测另记状态、门槛结论和启用记录。
type DramaEvalRun struct {
	ID               string `json:"id" gorm:"primaryKey;size:36"`
	Stage            string `json:"stage" gorm:"size:24;index"`
	ProjectID        string `json:"projectId" gorm:"size:36;index"`
	UnitIDsJSON      string `json:"-" gorm:"type:text"`
	Model            string `json:"model" gorm:"size:64"`
	CandidateVersion int    `json:"candidateVersion"`
	BaselineVersion  int    `json:"baselineVersion"`
	ResultJSON       string `json:"-" gorm:"type:text"`
	// SkillID / SkillVersionID 是步骤评测的技能包与候选版本；BaselineSkillVersionID 是对照的启用版本（评测开始时的）。
	SkillID                string `json:"skillId,omitempty" gorm:"size:36;index:idx_drama_eval_runs_skill_version,priority:1"`
	SkillVersionID         string `json:"skillVersionId,omitempty" gorm:"size:36;index:idx_drama_eval_runs_skill_version,priority:2"`
	BaselineSkillVersionID string `json:"baselineSkillVersionId,omitempty" gorm:"size:36"`
	// Status 是步骤评测的进度：queued、running、passed、failed（未过门槛）、error（没跑完，可重跑）；compile 评测为空。
	Status string `json:"status,omitempty" gorm:"size:16;index"`
	// TasksJSON 是逐章的任务位（章节、候选或对照、任务 ID、重投记录）；GateJSON 是门槛结论。
	// ModelJSON 是评测用的文本模型选择（任务按并发上限逐个投出时用），config 里的密钥按任务表同一规则加密。
	ModelJSON string `json:"-" gorm:"type:text"`
	TasksJSON string `json:"-" gorm:"type:text"`
	GateJSON  string `json:"-" gorm:"type:text"`
	Error     string `json:"error,omitempty" gorm:"type:text"`
	// Activation 是评测后的启用记录：auto（过门槛自动启用）、forced（管理员强制启用，ActivationReason 写原因）；空表示没启用。
	Activation       string     `json:"activation,omitempty" gorm:"size:16"`
	ActivationReason string     `json:"activationReason,omitempty" gorm:"type:text"`
	ActivatedBy      string     `json:"activatedBy,omitempty" gorm:"size:36"`
	ActivatedAt      *time.Time `json:"activatedAt,omitempty"`
	FinishedAt       *time.Time `json:"finishedAt,omitempty"`
	CreatedBy        string     `json:"createdBy" gorm:"size:36"`
	CreatedAt        time.Time  `json:"createdAt" gorm:"index"`
}

// DramaStepBinding 是「风格档案 × 步骤 → 技能」的一格（M6）：运行时按项目风格档案取这一格技能的当前版本；
// 没配的格子用步骤表里该步骤的默认技能。
type DramaStepBinding struct {
	ID           string    `json:"id" gorm:"primaryKey;size:36"`
	StyleProfile string    `json:"styleProfile" gorm:"size:32;not null;uniqueIndex:idx_drama_step_binding,priority:1"`
	Step         string    `json:"step" gorm:"size:40;not null;uniqueIndex:idx_drama_step_binding,priority:2"`
	SkillID      string    `json:"skillId" gorm:"size:36;not null"`
	UpdatedBy    string    `json:"updatedBy" gorm:"size:36"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// DramaStyleLibraryEntry 是平台风格库的一条（M8）：建项目时锁定的风格（风格锚 + 主基调色卡），按项目类型归档，团队共享——
// 登录用户都能看、能新增；改名、改色板、删除限管理员或创建者。项目选用时把风格锚和色板复制进自己的 bible_json，条目之后改了或删了不影响已选的项目。
type DramaStyleLibraryEntry struct {
	ID string `json:"id" gorm:"primaryKey;size:36"`
	// ProjectType 是画面类型：live_action / cg_real / anime_3d / anime_2d（与项目 visual_type 同值）。
	ProjectType string `json:"projectType" gorm:"size:32;not null;index:idx_drama_style_library_type,priority:1;uniqueIndex:idx_drama_style_library_name,priority:1"`
	// Name 在同一项目类型里不重名（唯一索引 idx_drama_style_library_name）。
	Name        string `json:"name" gorm:"size:120;not null;uniqueIndex:idx_drama_style_library_name,priority:2"`
	Description string `json:"description" gorm:"size:500"`
	// StyleAnchorJSON / MasterPaletteJSON 是风格锚和主基调色卡（与 bible_json.style_anchor / master_palette 同一形状）。
	StyleAnchorJSON   string `json:"styleAnchorJson" gorm:"type:text"`
	MasterPaletteJSON string `json:"masterPaletteJson" gorm:"type:text"`
	// CoverResourceID 是合成的主色卡图（资源属于合成它的账号；所有登录用户经 /drama/style-library/:id/cover 读）。
	CoverResourceID string `json:"coverResourceId" gorm:"size:36"`
	// Source 是 director（建项时交给导演分析、锁定后自动存入，说明里写项目名）/ analysis（风格分析后手动存入）/ manual（后台手工建）/ seed（预置）。
	Source    string    `json:"source" gorm:"size:16"`
	CreatedBy string    `json:"createdBy" gorm:"size:36;index"`
	UpdatedBy string    `json:"updatedBy" gorm:"size:36"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt" gorm:"index:idx_drama_style_library_type,priority:2"`
}

func (DramaStyleLibraryEntry) TableName() string { return "drama_style_library" }

type ShotAssetReference struct {
	ID             string    `json:"id" gorm:"primaryKey;size:36"`
	ShotID         string    `json:"shotId" gorm:"index;size:36;uniqueIndex:idx_shot_asset_reference_unique,priority:1"`
	AssetVersionID string    `json:"assetVersionId" gorm:"index;size:36;uniqueIndex:idx_shot_asset_reference_unique,priority:2"`
	Role           string    `json:"role" gorm:"index;size:32;uniqueIndex:idx_shot_asset_reference_unique,priority:3"`
	Status         string    `json:"status" gorm:"index;size:24"`
	CreatedAt      time.Time `json:"createdAt"`
}

type WorkflowTemplateVersion struct {
	ID             string    `json:"id" gorm:"primaryKey;size:36"`
	TemplateKey    string    `json:"templateKey" gorm:"size:80;uniqueIndex:idx_workflow_template_version,priority:1"`
	Name           string    `json:"name" gorm:"size:160"`
	Version        int       `json:"version" gorm:"uniqueIndex:idx_workflow_template_version,priority:2"`
	DefinitionJSON string    `json:"definitionJson" gorm:"type:text"`
	CreatedAt      time.Time `json:"createdAt"`
}

type WorkflowInstance struct {
	ID                string         `json:"id" gorm:"primaryKey;size:36"`
	ProjectID         string         `json:"projectId" gorm:"index;size:36;uniqueIndex:idx_workflow_instance_scope,priority:1"`
	UnitID            string         `json:"unitId,omitempty" gorm:"index;size:36;uniqueIndex:idx_workflow_instance_scope,priority:2"`
	TemplateVersionID string         `json:"templateVersionId" gorm:"index;size:36;uniqueIndex:idx_workflow_instance_scope,priority:3"`
	Scope             string         `json:"scope" gorm:"index;size:24"`
	Status            WorkflowStatus `json:"status" gorm:"index;size:24"`
	Revision          int64          `json:"revision"`
	CreatedAt         time.Time      `json:"createdAt"`
	UpdatedAt         time.Time      `json:"updatedAt"`
}

type WorkflowStepInstance struct {
	ID                 string             `json:"id" gorm:"primaryKey;size:36"`
	WorkflowInstanceID string             `json:"workflowInstanceId" gorm:"index;size:36;uniqueIndex:idx_workflow_steps_instance_key,priority:1"`
	StepKey            string             `json:"stepKey" gorm:"size:80;uniqueIndex:idx_workflow_steps_instance_key,priority:2"`
	Name               string             `json:"name" gorm:"size:160"`
	Position           int                `json:"position"`
	Status             WorkflowStepStatus `json:"status" gorm:"index;size:24"`
	InputJSON          string             `json:"inputJson" gorm:"type:text"`
	OutputJSON         string             `json:"outputJson" gorm:"type:text"`
	Error              string             `json:"error" gorm:"type:text"`
	StartedAt          *time.Time         `json:"startedAt"`
	CompletedAt        *time.Time         `json:"completedAt"`
	CreatedAt          time.Time          `json:"createdAt"`
	UpdatedAt          time.Time          `json:"updatedAt"`
}

type WorkflowStepTask struct {
	ID             string    `json:"id" gorm:"primaryKey;size:36"`
	WorkflowStepID string    `json:"workflowStepId" gorm:"index;size:36;uniqueIndex:idx_workflow_step_tasks_unique,priority:1"`
	TaskID         string    `json:"taskId" gorm:"index;size:36;uniqueIndex:idx_workflow_step_tasks_unique,priority:2"`
	CreatedAt      time.Time `json:"createdAt"`
}

// ProductionTaskLink 显式区分领域项目、画布和镜头上下文，避免继续复用 Task.ProjectID 表达多种身份。
type ProductionTaskLink struct {
	ID             string    `json:"id" gorm:"primaryKey;size:36"`
	TaskID         string    `json:"taskId" gorm:"index;size:36;uniqueIndex:idx_production_task_context,priority:1"`
	ProjectID      string    `json:"projectId" gorm:"index;size:36"`
	CanvasID       string    `json:"canvasId,omitempty" gorm:"index;size:80"`
	UnitID         string    `json:"unitId,omitempty" gorm:"index;size:36"`
	ShotID         string    `json:"shotId,omitempty" gorm:"index;size:36;uniqueIndex:idx_production_task_context,priority:2"`
	WorkflowStepID string    `json:"workflowStepId,omitempty" gorm:"index;size:36"`
	ArtifactType   string    `json:"artifactType,omitempty" gorm:"index;size:40;uniqueIndex:idx_production_task_context,priority:3"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type CanvasProject struct {
	ID          string    `json:"id" gorm:"primaryKey;size:80"`
	UserID      string    `json:"userId" gorm:"index;size:36;index:idx_canvas_projects_user_updated,priority:1;index:idx_canvas_projects_user_project_updated,priority:1"`
	ProjectID   string    `json:"projectId,omitempty" gorm:"index;size:36;index:idx_canvas_projects_user_project_updated,priority:2"`
	Title       string    `json:"title" gorm:"size:240"`
	PayloadJSON string    `json:"payloadJson" gorm:"type:text"`
	Revision    int64     `json:"revision" gorm:"not null;default:1"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt" gorm:"index:idx_canvas_projects_user_updated,priority:2;index:idx_canvas_projects_user_project_updated,priority:3"`
}

type CanvasShare struct {
	ID          string     `json:"id" gorm:"primaryKey;size:36"`
	UserID      string     `json:"userId" gorm:"index;size:36;uniqueIndex:idx_canvas_share_owner_project,priority:1"`
	ProjectID   string     `json:"projectId" gorm:"index;size:80;uniqueIndex:idx_canvas_share_owner_project,priority:2"`
	TokenHash   string     `json:"-" gorm:"uniqueIndex;size:64"`
	TokenCipher string     `json:"-" gorm:"type:text"`
	Enabled     bool       `json:"enabled" gorm:"index"`
	ExpiresAt   *time.Time `json:"expiresAt" gorm:"index"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

type PromptTemplate struct {
	ID         string    `json:"id" gorm:"primaryKey;size:36"`
	Operation  string    `json:"operation" gorm:"size:64;index;uniqueIndex:idx_prompt_template_operation_version,priority:1"`
	Name       string    `json:"name" gorm:"size:120"`
	Version    int       `json:"version" gorm:"uniqueIndex:idx_prompt_template_operation_version,priority:2"`
	Content    string    `json:"content" gorm:"type:text"`
	OutputType string    `json:"outputType" gorm:"size:24"`
	Enabled    bool      `json:"enabled" gorm:"index;index:idx_prompt_template_active,priority:2"`
	CreatedBy  string    `json:"createdBy" gorm:"index;size:36"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// UserPromptCustomization 只保存用户的创作策略层，动态上下文和输出契约始终由服务端编译器注入。
type UserPromptCustomization struct {
	ID             string    `json:"id" gorm:"primaryKey;size:36"`
	UserID         string    `json:"userId" gorm:"size:36;index;uniqueIndex:idx_user_prompt_operation,priority:1"`
	Operation      string    `json:"operation" gorm:"size:64;index;uniqueIndex:idx_user_prompt_operation,priority:2"`
	Mode           string    `json:"mode" gorm:"size:24"`
	Content        string    `json:"content" gorm:"type:text"`
	BaseTemplateID string    `json:"baseTemplateId" gorm:"size:36;index"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type Announcement struct {
	ID              string             `json:"id" gorm:"primaryKey;size:36"`
	Title           string             `json:"title" gorm:"size:120"`
	Content         string             `json:"content" gorm:"type:text"`
	ImageResourceID string             `json:"imageResourceId,omitempty" gorm:"index;size:36"`
	ImageURL        string             `json:"imageUrl,omitempty" gorm:"-"`
	Level           AnnouncementLevel  `json:"level" gorm:"index;size:24"`
	Pinned          bool               `json:"pinned" gorm:"index"`
	Status          AnnouncementStatus `json:"status" gorm:"index;size:24;index:idx_announcements_status_published,priority:1"`
	CreatedBy       string             `json:"createdBy" gorm:"index;size:36"`
	PublishedAt     time.Time          `json:"publishedAt" gorm:"index:idx_announcements_status_published,priority:2"`
	ClosedAt        *time.Time         `json:"closedAt"`
	CreatedAt       time.Time          `json:"createdAt"`
	UpdatedAt       time.Time          `json:"updatedAt"`
}

type UserAnnouncementRead struct {
	ID             string    `json:"id" gorm:"primaryKey;size:36"`
	UserID         string    `json:"userId" gorm:"index;size:36;uniqueIndex:idx_user_announcement_read,priority:1"`
	AnnouncementID string    `json:"announcementId" gorm:"index;size:36;uniqueIndex:idx_user_announcement_read,priority:2"`
	ReadAt         time.Time `json:"readAt"`
}

// BannerTitleRun 是通知标题中一段连续文本的样式覆盖；零值字段表示沿用默认样式。
// 字重与字号用指针区分「未设置」和「显式设置为默认值」。
type BannerTitleRun struct {
	Text       string `json:"text"`
	FontSize   *int   `json:"fontSize,omitempty"`   // px，仅接受 10-20
	FontWeight *int   `json:"fontWeight,omitempty"` // 仅接受 400 / 500 / 600 / 700
	FontFamily string `json:"fontFamily,omitempty"` // "" | "sans" | "serif" | "mono"
	Color      string `json:"color,omitempty"`      // #RRGGBB
}

type BannerAnnouncement struct {
	ID    string `json:"id" gorm:"primaryKey;size:36"`
	Title string `json:"title" gorm:"size:120"` // 纯文本标题，由 TitleRuns 拼接得出，供列表展示和关键字检索
	// TitleRuns 是标题的样式分段，前端按段渲染；持久化在 title_runs 文本列，由仓储层显式编解码。
	TitleRuns     []BannerTitleRun `json:"titleRuns,omitempty" gorm:"-"`
	TitleRunsJSON string           `json:"-" gorm:"column:title_runs;type:text"`
	// NoticeType 决定通知条底色（"notice" | "activity" | "update" | "warning"），取值白名单见 app 层。
	NoticeType string     `json:"noticeType" gorm:"size:24"`
	Link       string     `json:"link" gorm:"size:500"`        // 点击跳转目标：http(s) 外链或 / 开头的站内路径；空表示不可点击
	Status     string     `json:"status" gorm:"size:24;index"` // "active" | "disabled"
	SortOrder  int        `json:"sortOrder" gorm:"index"`
	StartsAt   *time.Time `json:"startsAt,omitempty" gorm:"index"`
	EndsAt     *time.Time `json:"endsAt,omitempty" gorm:"index"`
	CreatedBy  string     `json:"createdBy" gorm:"size:36"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}
