package accounts

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/Autonex009/autonex-crm-api/pkg/apperr"
	"github.com/Autonex009/autonex-crm-api/pkg/database"
	"github.com/Autonex009/autonex-crm-api/pkg/httpx"
	"github.com/Autonex009/autonex-crm-api/pkg/middleware"
)

// A company's sites, as records rather than a JSONB blob.
//
// They were `account_profiles.plant_locations` — a free-form array edited only
// in the Company Profile. A deal recorded its site as free text because there
// was no id to point at, which meant renaming a plant left every deal that
// named it pointing at a string that no longer existed anywhere. Migration
// 000023 promoted them and backfilled one row per distinct site already named
// by a profile or a deal, so nothing that was typed is lost.
//
// plant_locations is still written by the old profile editor and still read
// here as a fallback; it is retired in a later migration, once the new editor
// has been in use long enough to trust.

var (
	// ErrLocationNotFound means no such site in the caller's org.
	ErrLocationNotFound = errors.New("location not found")
	// ErrLocationTaken means the company already has a site by that name.
	ErrLocationTaken = errors.New("location name already used")
)

const locationNameIdx = "account_locations_name_idx"

// Location is one site belonging to a company.
type Location struct {
	ID         string     `json:"id"`
	AccountID  string     `json:"accountId"`
	Name       string     `json:"name"`
	City       *string    `json:"city"`
	Address    *string    `json:"address"`
	SpocName   *string    `json:"spocName"`
	SpocPhone  *string    `json:"spocPhone"`
	Position   int        `json:"position"`
	ArchivedAt *time.Time `json:"archivedAt"`
	// DealCount is how many live deals name this site. The profile shows it so
	// archiving is a decision with the consequence in view.
	DealCount int `json:"dealCount"`
}

// LocationInput is the writable shape of a site. Only the name is required: a
// plant is worth recording before anyone has found out who runs it.
type LocationInput struct {
	Name      string  `json:"name"`
	City      *string `json:"city"`
	Address   *string `json:"address"`
	SpocName  *string `json:"spocName"`
	SpocPhone *string `json:"spocPhone"`
}

const locationColumns = `
	l.id::text, l.account_id::text, l.name, l.city, l.address,
	l.spoc_name, l.spoc_phone, l.position, l.archived_at,
	-- Counted through deal_locations, not a column on deals: a deal delivers to
	-- several sites (migration 000024), so the count is of the links.
	(SELECT count(*) FROM deal_locations dl
	   JOIN deals d ON d.id = dl.deal_id AND d.deleted_at IS NULL
	  WHERE dl.location_id = l.id)::int`

// listLocations returns a company's sites, live ones first.
//
// orgID is accepted and ignored, the same way every other read in this package
// does it: the accounts table carries no org column in this schema, so there is
// nothing to scope against here. The parameter stays in the signature so these
// calls need no rewriting when accounts does become org-scoped — see the
// package comment in store.go.
func (s *store) listLocations(ctx context.Context, _, accountID string, includeArchived bool) ([]Location, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+locationColumns+`
		   FROM account_locations l
		   JOIN accounts a ON a.id = l.account_id AND a.deleted_at IS NULL
		  WHERE l.account_id = $1
		    AND ($2::boolean OR l.archived_at IS NULL)
		  ORDER BY l.archived_at IS NOT NULL, l.position, lower(l.name)`,
		accountID, includeArchived)
	if err != nil {
		if database.IsInvalidTextRepr(err) {
			return []Location{}, nil
		}
		return nil, err
	}
	defer rows.Close()

	out := make([]Location, 0, 8)
	for rows.Next() {
		loc, err := scanLocation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, loc)
	}
	return out, rows.Err()
}

func (s *store) createLocation(ctx context.Context, _, accountID string, in LocationInput) (Location, error) {
	row := s.pool.QueryRow(ctx,
		`WITH target AS (
		   SELECT id FROM accounts WHERE id = $1 AND deleted_at IS NULL
		 ), inserted AS (
		   INSERT INTO account_locations
		     (account_id, name, city, address, spoc_name, spoc_phone, position)
		   SELECT t.id, $2, $3, $4, $5, $6,
		          COALESCE((SELECT max(position) + 1 FROM account_locations
		                     WHERE account_id = t.id), 0)
		     FROM target t
		   RETURNING *
		 )
		 SELECT `+locationColumns+` FROM inserted l`,
		accountID, in.Name, in.City, in.Address, in.SpocName, in.SpocPhone)
	return scanLocation(row)
}

func (s *store) updateLocation(ctx context.Context, _, accountID, id string, in LocationInput) (Location, error) {
	row := s.pool.QueryRow(ctx,
		`WITH updated AS (
		   UPDATE account_locations l
		      SET name = $3, city = $4, address = $5, spoc_name = $6,
		          spoc_phone = $7, updated_at = now()
		    WHERE l.id = $2
		      AND l.account_id = $1
		   RETURNING *
		 )
		 SELECT `+locationColumns+` FROM updated l`,
		accountID, id, in.Name, in.City, in.Address, in.SpocName, in.SpocPhone)
	return scanLocation(row)
}

// archiveLocation retires a site without deleting it.
//
// A deal that was delivered to a plant still has to be able to say so, and
// deal_locations cascades on delete — a hard delete would quietly drop the site
// off historical deals. Archived sites leave the pickers and stay on the
// records that already chose them.
func (s *store) archiveLocation(ctx context.Context, _, accountID, id string, archived bool) (Location, error) {
	row := s.pool.QueryRow(ctx,
		`WITH updated AS (
		   UPDATE account_locations l
		      SET archived_at = CASE WHEN $3 THEN now() ELSE NULL END,
		          updated_at = now()
		    WHERE l.id = $2
		      AND l.account_id = $1
		   RETURNING *
		 )
		 SELECT `+locationColumns+` FROM updated l`,
		accountID, id, archived)
	return scanLocation(row)
}

func scanLocation(row pgx.Row) (Location, error) {
	var l Location
	err := row.Scan(&l.ID, &l.AccountID, &l.Name, &l.City, &l.Address,
		&l.SpocName, &l.SpocPhone, &l.Position, &l.ArchivedAt, &l.DealCount)
	switch {
	case errors.Is(err, pgx.ErrNoRows), database.IsInvalidTextRepr(err):
		return Location{}, ErrLocationNotFound
	case database.IsUniqueViolationOn(err, locationNameIdx):
		return Location{}, ErrLocationTaken
	case err != nil:
		return Location{}, err
	}
	return l, nil
}

// --- service ---

func normalizeLocation(in LocationInput) LocationInput {
	in.Name = strings.TrimSpace(in.Name)
	in.City = trimmedOrNil(in.City)
	in.Address = trimmedOrNil(in.Address)
	in.SpocName = trimmedOrNil(in.SpocName)
	in.SpocPhone = trimmedOrNil(in.SpocPhone)
	return in
}

func validateLocation(in LocationInput) error {
	if in.Name == "" {
		return apperr.Invalid("a location needs a name")
	}
	if len([]rune(in.Name)) > 200 {
		return apperr.Invalid("location name is too long")
	}
	return nil
}

// ListLocations returns a company's sites for the profile and the pickers.
func (s *Service) ListLocations(ctx context.Context, orgID, accountID string, includeArchived bool) ([]Location, error) {
	return s.store.listLocations(ctx, orgID, accountID, includeArchived)
}

func (s *Service) CreateLocation(ctx context.Context, orgID, accountID string, in LocationInput) (Location, error) {
	in = normalizeLocation(in)
	if err := validateLocation(in); err != nil {
		return Location{}, err
	}
	return s.store.createLocation(ctx, orgID, accountID, in)
}

func (s *Service) UpdateLocation(ctx context.Context, orgID, accountID, id string, in LocationInput) (Location, error) {
	in = normalizeLocation(in)
	if err := validateLocation(in); err != nil {
		return Location{}, err
	}
	return s.store.updateLocation(ctx, orgID, accountID, id, in)
}

func (s *Service) ArchiveLocation(ctx context.Context, orgID, accountID, id string, archived bool) (Location, error) {
	return s.store.archiveLocation(ctx, orgID, accountID, id, archived)
}

// --- handlers ---

func (h *Handler) listLocations(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.ListLocations(r.Context(), middleware.OrgID(r.Context()),
		chi.URLParam(r, "id"), r.URL.Query().Get("includeArchived") == "true")
	if err != nil {
		h.writeLocationErr(w, err, "could not load those locations")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) createLocation(w http.ResponseWriter, r *http.Request) {
	var in LocationInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	loc, err := h.svc.CreateLocation(r.Context(), middleware.OrgID(r.Context()),
		chi.URLParam(r, "id"), in)
	if err != nil {
		h.writeLocationErr(w, err, "could not add that location")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, loc)
}

func (h *Handler) updateLocation(w http.ResponseWriter, r *http.Request) {
	var in LocationInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	loc, err := h.svc.UpdateLocation(r.Context(), middleware.OrgID(r.Context()),
		chi.URLParam(r, "id"), chi.URLParam(r, "locationId"), in)
	if err != nil {
		h.writeLocationErr(w, err, "could not save that location")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, loc)
}

// archiveLocation doubles as un-archive, so the profile's toggle is one route.
func (h *Handler) archiveLocation(w http.ResponseWriter, r *http.Request) {
	archived := r.URL.Query().Get("archived") != "false"
	loc, err := h.svc.ArchiveLocation(r.Context(), middleware.OrgID(r.Context()),
		chi.URLParam(r, "id"), chi.URLParam(r, "locationId"), archived)
	if err != nil {
		h.writeLocationErr(w, err, "could not archive that location")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, loc)
}

func (h *Handler) writeLocationErr(w http.ResponseWriter, err error, fallback string) {
	httpx.WriteDomainError(w, err, fallback,
		httpx.Rule{Err: ErrLocationNotFound, Status: http.StatusNotFound,
			Message: "that location no longer exists"},
		httpx.Rule{Err: ErrLocationTaken, Status: http.StatusConflict,
			Message: "this company already has a location with that name"},
	)
}
