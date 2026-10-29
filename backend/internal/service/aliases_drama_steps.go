package service

import "yingce/backend/internal/app"

type (
	DramaProfileRef          = app.DramaProfileRef
	DramaProfiles            = app.DramaProfiles
	DramaStepSkillRef        = app.DramaStepSkillRef
	DramaStepUsage           = app.DramaStepUsage
	DramaStepRunRequest      = app.DramaStepRunRequest
	DramaStepRunResult       = app.DramaStepRunResult
	DramaSectionGenreRequest = app.DramaSectionGenreRequest
	DramaSectionGenreResult  = app.DramaSectionGenreResult
	DramaStepPrompt          = app.DramaStepPrompt
	DramaStepBindingCell     = app.DramaStepBindingCell
	DramaStepBindingMatrix   = app.DramaStepBindingMatrix
	DramaStepBindingsRequest = app.DramaStepBindingsRequest

	DramaProjectStepRunRequest      = app.DramaProjectStepRunRequest
	DramaScriptAdaptOutcome         = app.DramaScriptAdaptOutcome
	DramaEpisodesView               = app.DramaEpisodesView
	DramaImageTaskTemplate          = app.DramaImageTaskTemplate
	DramaScenePaletteOutcome        = app.DramaScenePaletteOutcome
	DramaPaletteResynthesizeRequest = app.DramaPaletteResynthesizeRequest
	DramaPaletteResynthesizeResult  = app.DramaPaletteResynthesizeResult
	DramaAssetImagesRequest         = app.DramaAssetImagesRequest
	DramaAssetsExtractOutcome       = app.DramaAssetsExtractOutcome
	DramaImageQueueResult           = app.DramaImageQueueResult
	DramaAssetVariantRequest        = app.DramaAssetVariantRequest
	DramaAssetVariantResult         = app.DramaAssetVariantResult

	DramaStyleLockRequest          = app.DramaStyleLockRequest
	DramaStyleLockResult           = app.DramaStyleLockResult
	DramaStyleLockOutcome          = app.DramaStyleLockOutcome
	DramaStyleLibraryItem          = app.DramaStyleLibraryItem
	DramaStyleLibraryList          = app.DramaStyleLibraryList
	DramaStyleLibrarySaveRequest   = app.DramaStyleLibrarySaveRequest
	DramaStyleLibraryUpdateRequest = app.DramaStyleLibraryUpdateRequest
)
