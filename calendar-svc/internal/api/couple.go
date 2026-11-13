package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/choigonyok/jarvis/calendar-svc/internal/event"
	"github.com/choigonyok/jarvis/calendar-svc/internal/uniple"
)

// Couple is the rest of the couple's space in uniple: the D-day, the bucket
// lists, the habit checks, the date map, free notes, what the two did, and
// the photos. Only the uniple backend has it.
type Couple interface {
	Space(ctx context.Context) (uniple.Space, error)
	Block(ctx context.Context, id string) (uniple.Block, error)
	Checks(ctx context.Context, block, from, to string) ([]uniple.Check, error)
	News(ctx context.Context, limit int) ([]uniple.News, error)
	DayPhotos(ctx context.Context, from, to string) ([]uniple.DayPhoto, error)
	Cover(ctx context.Context, date string) (*uniple.DayPhoto, error)
	Photo(ctx context.Context, id, size string) (uniple.Photo, error)

	AddBlock(ctx context.Context, kind, title string) (uniple.Block, error)
	RenameBlock(ctx context.Context, id, title string) error
	AddDDay(ctx context.Context, block string, in uniple.DDayInput) (uniple.DDay, error)
	SetDDay(ctx context.Context, block, id string, in uniple.DDayInput) (uniple.DDay, error)
	DeleteDDay(ctx context.Context, block, id string) error
	AddBucket(ctx context.Context, block, title string) (uniple.Bucket, error)
	SetBucket(ctx context.Context, block, id string, in uniple.BucketInput) (uniple.Bucket, error)
	DeleteBucket(ctx context.Context, block, id string) error
	AddFree(ctx context.Context, block, content string) (uniple.Free, error)
	DeleteFree(ctx context.Context, block, id string) error
	Check(ctx context.Context, block, date, memo string) (uniple.Check, error)
	SetCheckMemo(ctx context.Context, block, id, memo string) error
	Uncheck(ctx context.Context, block, id string) error
	AddRecord(ctx context.Context, block string, in uniple.RecordInput) (uniple.Record, error)
	SetRecord(ctx context.Context, block, id string, in uniple.RecordInput) (uniple.Record, error)
	DeleteRecord(ctx context.Context, block, id string) error

	Upload(ctx context.Context, block, kind string, body []byte) (string, error)
	AddVisit(ctx context.Context, block string, in uniple.VisitInput) (uniple.Visit, error)
	SetVisit(ctx context.Context, block, id, asset string) (uniple.Visit, error)
	DeleteVisit(ctx context.Context, block, id string) error
	AddDayPhoto(ctx context.Context, in uniple.DayPhotoInput) (uniple.DayPhoto, error)
	DeleteDayPhoto(ctx context.Context, id string) error
	CoverDayPhoto(ctx context.Context, id string) error
	UpdateBlock(ctx context.Context, id string, in uniple.BlockInput) error
	ArchiveBlock(ctx context.Context, id string) error
	OrderBlocks(ctx context.Context, ids []string) error
	SetStart(ctx context.Context, block, title, date string) error
}

var _ Couple = (*uniple.Client)(nil)

// WithCouple opens the couple's space next to the calendar.
func (s *Server) WithCouple(c Couple) *Server {
	s.couple = c
	return s
}

func (s *Server) coupleRoutes(mux *http.ServeMux) {
	read := func(pattern string, h http.HandlerFunc) { mux.HandleFunc("GET "+pattern, s.auth(s.owner(h))) }
	write := func(pattern string, h http.HandlerFunc) { mux.HandleFunc(pattern, s.auth(s.owner(h))) }

	read("/couple", func(w http.ResponseWriter, r *http.Request) {
		space, err := s.couple.Space(r.Context())
		s.answer(w, space, err)
	})
	read("/couple/blocks/{block}", func(w http.ResponseWriter, r *http.Request) {
		b, err := s.couple.Block(r.Context(), r.PathValue("block"))
		s.answer(w, b, err)
	})
	read("/couple/blocks/{block}/checks", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		checks, err := s.couple.Checks(r.Context(), r.PathValue("block"), q.Get("from"), q.Get("to"))
		s.answer(w, map[string]any{"checks": checks}, err)
	})
	read("/couple/news", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		news, err := s.couple.News(r.Context(), limit)
		s.answer(w, map[string]any{"news": news}, err)
	})
	read("/couple/photos", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		photos, err := s.couple.DayPhotos(r.Context(), q.Get("from"), q.Get("to"))
		s.answer(w, map[string]any{"photos": photos}, err)
	})
	read("/couple/cover", func(w http.ResponseWriter, r *http.Request) {
		cover, err := s.couple.Cover(r.Context(), r.URL.Query().Get("date"))
		s.answer(w, map[string]any{"cover": cover}, err)
	})
	read("/couple/media/{id}", s.photo)

	write("POST /couple/blocks", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Type  string `json:"type"`
			Title string `json:"title"`
		}
		if decode(w, r, &in) {
			b, err := s.couple.AddBlock(r.Context(), in.Type, in.Title)
			s.answer(w, b, err)
		}
	})
	write("PATCH /couple/blocks/{block}", func(w http.ResponseWriter, r *http.Request) {
		var in uniple.BlockInput
		if decode(w, r, &in) {
			s.answer(w, ok, s.couple.UpdateBlock(r.Context(), r.PathValue("block"), in))
		}
	})
	write("DELETE /couple/blocks/{block}", func(w http.ResponseWriter, r *http.Request) {
		s.answer(w, ok, s.couple.ArchiveBlock(r.Context(), r.PathValue("block")))
	})
	write("POST /couple/order", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			IDs []string `json:"ids"`
		}
		if decode(w, r, &in) {
			s.answer(w, ok, s.couple.OrderBlocks(r.Context(), in.IDs))
		}
	})

	// A photo, uploaded first and put somewhere after: the bytes do not go
	// through the way in, and a photo nothing names shows nowhere.
	write("POST /couple/uploads", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, uniple.MaxPhoto+1))
		if err != nil {
			writeErr(w, http.StatusRequestEntityTooLarge, "사진은 4MB 까지 올릴 수 있어요.")
			return
		}
		kind, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";")
		asset, err := s.couple.Upload(r.Context(), r.URL.Query().Get("block"), strings.TrimSpace(kind), body)
		s.answer(w, map[string]string{"asset": asset}, err)
	})
	write("POST /couple/blocks/{block}/visits", func(w http.ResponseWriter, r *http.Request) {
		var in uniple.VisitInput
		if decode(w, r, &in) {
			v, err := s.couple.AddVisit(r.Context(), r.PathValue("block"), in)
			s.answer(w, v, err)
		}
	})
	write("PATCH /couple/blocks/{block}/visits/{item}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Asset string `json:"asset"`
		}
		if decode(w, r, &in) {
			v, err := s.couple.SetVisit(r.Context(), r.PathValue("block"), r.PathValue("item"), in.Asset)
			s.answer(w, v, err)
		}
	})
	write("DELETE /couple/blocks/{block}/visits/{item}", func(w http.ResponseWriter, r *http.Request) {
		s.answer(w, ok, s.couple.DeleteVisit(r.Context(), r.PathValue("block"), r.PathValue("item")))
	})
	write("POST /couple/photos", func(w http.ResponseWriter, r *http.Request) {
		var in uniple.DayPhotoInput
		if decode(w, r, &in) {
			p, err := s.couple.AddDayPhoto(r.Context(), in)
			s.answer(w, p, err)
		}
	})
	write("DELETE /couple/photos/{item}", func(w http.ResponseWriter, r *http.Request) {
		s.answer(w, ok, s.couple.DeleteDayPhoto(r.Context(), r.PathValue("item")))
	})
	write("POST /couple/photos/{item}/cover", func(w http.ResponseWriter, r *http.Request) {
		s.answer(w, ok, s.couple.CoverDayPhoto(r.Context(), r.PathValue("item")))
	})
	write("POST /couple/blocks/{block}/start", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Title string `json:"title"`
			Date  string `json:"date"`
		}
		if decode(w, r, &in) {
			s.answer(w, ok, s.couple.SetStart(r.Context(), r.PathValue("block"), in.Title, in.Date))
		}
	})

	write("POST /couple/blocks/{block}/dday", func(w http.ResponseWriter, r *http.Request) {
		var in uniple.DDayInput
		if decode(w, r, &in) {
			d, err := s.couple.AddDDay(r.Context(), r.PathValue("block"), in)
			s.answer(w, d, err)
		}
	})
	write("PATCH /couple/blocks/{block}/dday/{item}", func(w http.ResponseWriter, r *http.Request) {
		var in uniple.DDayInput
		if decode(w, r, &in) {
			d, err := s.couple.SetDDay(r.Context(), r.PathValue("block"), r.PathValue("item"), in)
			s.answer(w, d, err)
		}
	})
	write("DELETE /couple/blocks/{block}/dday/{item}", func(w http.ResponseWriter, r *http.Request) {
		s.answer(w, ok, s.couple.DeleteDDay(r.Context(), r.PathValue("block"), r.PathValue("item")))
	})

	write("POST /couple/blocks/{block}/bucket", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Title string `json:"title"`
		}
		if decode(w, r, &in) {
			b, err := s.couple.AddBucket(r.Context(), r.PathValue("block"), in.Title)
			s.answer(w, b, err)
		}
	})
	write("PATCH /couple/blocks/{block}/bucket/{item}", func(w http.ResponseWriter, r *http.Request) {
		var in uniple.BucketInput
		if decode(w, r, &in) {
			b, err := s.couple.SetBucket(r.Context(), r.PathValue("block"), r.PathValue("item"), in)
			s.answer(w, b, err)
		}
	})
	write("DELETE /couple/blocks/{block}/bucket/{item}", func(w http.ResponseWriter, r *http.Request) {
		s.answer(w, ok, s.couple.DeleteBucket(r.Context(), r.PathValue("block"), r.PathValue("item")))
	})

	write("POST /couple/blocks/{block}/free", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Content string `json:"content"`
		}
		if decode(w, r, &in) {
			f, err := s.couple.AddFree(r.Context(), r.PathValue("block"), in.Content)
			s.answer(w, f, err)
		}
	})
	write("DELETE /couple/blocks/{block}/free/{item}", func(w http.ResponseWriter, r *http.Request) {
		s.answer(w, ok, s.couple.DeleteFree(r.Context(), r.PathValue("block"), r.PathValue("item")))
	})

	write("POST /couple/blocks/{block}/checks", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Date string `json:"date"`
			Memo string `json:"memo"`
		}
		if decode(w, r, &in) {
			c, err := s.couple.Check(r.Context(), r.PathValue("block"), in.Date, in.Memo)
			s.answer(w, c, err)
		}
	})
	write("PATCH /couple/blocks/{block}/checks/{item}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Memo string `json:"memo"`
		}
		if decode(w, r, &in) {
			s.answer(w, ok, s.couple.SetCheckMemo(r.Context(), r.PathValue("block"), r.PathValue("item"), in.Memo))
		}
	})
	write("DELETE /couple/blocks/{block}/checks/{item}", func(w http.ResponseWriter, r *http.Request) {
		s.answer(w, ok, s.couple.Uncheck(r.Context(), r.PathValue("block"), r.PathValue("item")))
	})

	write("POST /couple/blocks/{block}/records", func(w http.ResponseWriter, r *http.Request) {
		var in uniple.RecordInput
		if decode(w, r, &in) {
			rec, err := s.couple.AddRecord(r.Context(), r.PathValue("block"), in)
			s.answer(w, rec, err)
		}
	})
	write("PATCH /couple/blocks/{block}/records/{item}", func(w http.ResponseWriter, r *http.Request) {
		var in uniple.RecordInput
		if decode(w, r, &in) {
			rec, err := s.couple.SetRecord(r.Context(), r.PathValue("block"), r.PathValue("item"), in)
			s.answer(w, rec, err)
		}
	})
	write("DELETE /couple/blocks/{block}/records/{item}", func(w http.ResponseWriter, r *http.Request) {
		s.answer(w, ok, s.couple.DeleteRecord(r.Context(), r.PathValue("block"), r.PathValue("item")))
	})
}

var ok = map[string]bool{"ok": true}

// owner keeps the couple's space to the operator's side for now: the guest's
// console has no screen for it, and uniple holds one session - the
// operator's - so a guest's check would be filed under the operator's name.
func (s *Server) owner(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.couple == nil {
			writeErr(w, http.StatusNotFound, "uniple 을 쓰지 않는 캘린더입니다.")
			return
		}
		if r.Header.Get("X-Jarvis-Side") == "partner" {
			writeErr(w, http.StatusForbidden, "손님 화면에는 아직 없습니다.")
			return
		}
		next(w, r)
	}
}

func decode(w http.ResponseWriter, r *http.Request, into any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(into); err != nil {
		writeErr(w, http.StatusBadRequest, "본문을 읽을 수 없습니다.")
		return false
	}
	return true
}

// answer writes what came back, or the error as the screen should read it.
func (s *Server) answer(w http.ResponseWriter, v any, err error) {
	var invalid event.ValidationError
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, v)
	case errors.As(err, &invalid):
		writeErr(w, http.StatusBadRequest, invalid.Error())
	case errors.Is(err, uniple.ErrBlock), errors.Is(err, event.ErrNotFound):
		writeErr(w, http.StatusNotFound, err.Error())
	default:
		s.log.Error("uniple 블록을 다루지 못했습니다", "err", err)
		writeErr(w, http.StatusBadGateway, "uniple 에서 처리하지 못했습니다.")
	}
}

// photo streams one size of a photo. An asset's size never changes under its
// id, so the browser may keep it.
func (s *Server) photo(w http.ResponseWriter, r *http.Request) {
	size := r.URL.Query().Get("size")
	if size != "thumb" {
		size = "display"
	}
	p, err := s.couple.Photo(r.Context(), r.PathValue("id"), size)
	if errors.Is(err, uniple.ErrNoPhoto) {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		s.log.Warn("사진을 읽지 못했습니다", "err", err)
		writeErr(w, http.StatusBadGateway, "사진을 읽지 못했습니다.")
		return
	}
	defer p.Body.Close()
	w.Header().Set("Content-Type", p.Type)
	if p.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(p.Size, 10))
	}
	w.Header().Set("Cache-Control", "private, max-age=604800, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, p.Body)
}
