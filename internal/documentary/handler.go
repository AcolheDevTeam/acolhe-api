package documentary

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc} }
func (h *Handler) Register(e *echo.Echo) {
	g := e.Group("/documentary")
	g.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Response().Header().Set("Cache-Control", "private, no-store")
			return next(c)
		}
	})
	g.GET("/patients", h.patients)
	g.GET("/patients/:patientId", h.notebooks)
	g.PUT("/patients/:patientId/:category", h.save)
	g.POST("/patients/:patientId/:category/restore", h.restore)
	g.GET("/notebooks/:id/versions", h.history)
	g.GET("/notebooks/:id/versions/:revision", h.version)
}
func respond(c echo.Context, v any, err error) error {
	if err == nil {
		return c.JSON(http.StatusOK, v)
	}
	code := http.StatusInternalServerError
	message := "não foi possível acessar o registro documental; tente novamente"
	switch {
	case errors.Is(err, ErrInput):
		code = 400
	case errors.Is(err, ErrForbidden), errors.Is(err, ErrReadOnly):
		code = 403
	case errors.Is(err, ErrNotFound):
		code = 404
	case errors.Is(err, ErrConflict):
		code = 409
	case errors.Is(err, ErrCrypto):
		code = 503
	}
	if code != 500 {
		message = err.Error()
	}
	return echo.NewHTTPError(code, message)
}
func pagination(c echo.Context) (int, int, error) {
	page, size := 1, 20
	var err error
	if value := c.QueryParam("page"); value != "" {
		page, err = strconv.Atoi(value)
		if err != nil {
			return 0, 0, ErrInput
		}
	}
	if value := c.QueryParam("pageSize"); value != "" {
		size, err = strconv.Atoi(value)
		if err != nil {
			return 0, 0, ErrInput
		}
	}
	if page < 1 || page > 1000000 || size < 1 || size > 100 {
		return 0, 0, ErrInput
	}
	return page, size, nil
}
func (h *Handler) patients(c echo.Context) error {
	p, s, err := pagination(c)
	if err != nil {
		return respond(c, nil, err)
	}
	v, err := h.svc.Patients(c.Request().Context(), p, s)
	return respond(c, v, err)
}
func (h *Handler) notebooks(c echo.Context) error {
	id, err := uuid.Parse(c.Param("patientId"))
	if err != nil {
		return respond(c, nil, ErrInput)
	}
	v, err := h.svc.Notebooks(c.Request().Context(), id)
	return respond(c, v, err)
}
func (h *Handler) history(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return respond(c, nil, ErrInput)
	}
	p, s, err := pagination(c)
	if err != nil {
		return respond(c, nil, err)
	}
	v, err := h.svc.History(c.Request().Context(), id, p, s)
	return respond(c, v, err)
}
func (h *Handler) version(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return respond(c, nil, ErrInput)
	}
	rev, err := strconv.Atoi(c.Param("revision"))
	if err != nil || rev < 1 {
		return respond(c, nil, ErrInput)
	}
	v, err := h.svc.GetVersion(c.Request().Context(), id, rev)
	return respond(c, v, err)
}
func decode(c echo.Context, v any) error {
	d := json.NewDecoder(http.MaxBytesReader(c.Response(), c.Request().Body, MaxContentBytes*6+1024))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		return ErrInput
	}
	return nil
}
func (h *Handler) save(c echo.Context) error {
	id, err := uuid.Parse(c.Param("patientId"))
	if err != nil {
		return respond(c, nil, ErrInput)
	}
	var body struct {
		Content          *string `json:"content"`
		ExpectedRevision *int    `json:"expectedRevision"`
	}
	if decode(c, &body) != nil || body.Content == nil || body.ExpectedRevision == nil {
		return respond(c, nil, ErrInput)
	}
	v, err := h.svc.Save(c.Request().Context(), id, c.Param("category"), *body.Content, *body.ExpectedRevision, nil)
	return respond(c, v, err)
}
func (h *Handler) restore(c echo.Context) error {
	id, err := uuid.Parse(c.Param("patientId"))
	if err != nil {
		return respond(c, nil, ErrInput)
	}
	var body struct {
		Revision         int  `json:"revision"`
		ExpectedRevision *int `json:"expectedRevision"`
	}
	if decode(c, &body) != nil || body.Revision < 1 || body.ExpectedRevision == nil {
		return respond(c, nil, ErrInput)
	}
	v, err := h.svc.Save(c.Request().Context(), id, c.Param("category"), "", *body.ExpectedRevision, &body.Revision)
	return respond(c, v, err)
}
