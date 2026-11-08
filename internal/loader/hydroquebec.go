package loader

import "context"

// LoadHydroQuebec loads one Hydro-Quebec bronze file.
func (l *Loader) LoadHydroQuebec(ctx context.Context, key string, force bool) (Result, error) {
	return l.Load(ctx, "hydroquebec", key, force)
}

// LoadAllHydroQuebec loads every Hydro-Quebec bronze file.
func (l *Loader) LoadAllHydroQuebec(ctx context.Context, force bool) ([]Result, error) {
	return l.LoadAll(ctx, "hydroquebec", force)
}
