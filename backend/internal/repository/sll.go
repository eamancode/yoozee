package repository

import "yingce/backend/internal/sllcatalog"

func (r *Repository) SLLCatalog() *sllcatalog.Store {
	return sllcatalog.NewStore(r.db)
}
