package server

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
	"github.com/akuity/kargo/pkg/api"
	"github.com/akuity/kargo/pkg/event"
	libFreight "github.com/akuity/kargo/pkg/freight"
	libhttp "github.com/akuity/kargo/pkg/http"
	"github.com/akuity/kargo/pkg/logging"
	"github.com/akuity/kargo/pkg/server/user"
)

// @id CreateFreight
// @Summary Create Freight
// @Description Create a Freight resource from an explicit set of artifacts that
// @Description the origin Warehouse subscribes to. Kargo assigns the name and,
// @Description when omitted, the alias.
// @Tags Core, Project-Level
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param project path string true "Project name"
// @Param body body kargoapi.Freight true "Only alias, origin, commits, images, charts and artifacts are honored"
// @Success 201 {object} kargoapi.Freight "Created Freight"
// @Router /v1beta1/projects/{project}/freight [post]
func (s *server) createFreight(c *gin.Context) {
	ctx := c.Request.Context()
	project := c.Param("project")

	var in kargoapi.Freight
	if err := c.ShouldBindJSON(&in); err != nil {
		_ = c.Error(libhttp.Error(
			fmt.Errorf("invalid request body: %w", err),
			http.StatusBadRequest,
		))
		return
	}

	// Only the fields a caller may legitimately choose are copied. Name,
	// labels, status, etc. are assigned by Kargo.
	f := &kargoapi.Freight{
		ObjectMeta: metav1.ObjectMeta{Namespace: project},
		Alias:      in.Alias,
		Origin:     in.Origin,
		Commits:    in.Commits,
		Images:     in.Images,
		Charts:     in.Charts,
		Artifacts:  in.Artifacts,
	}
	if f.Origin.Kind == "" {
		f.Origin.Kind = kargoapi.FreightOriginKindWarehouse
	}
	if f.Origin.Kind != kargoapi.FreightOriginKindWarehouse || f.Origin.Name == "" {
		_ = c.Error(libhttp.ErrorStr(
			"origin must name a Warehouse",
			http.StatusBadRequest,
		))
		return
	}
	if len(f.Commits)+len(f.Images)+len(f.Charts)+len(f.Artifacts) == 0 {
		_ = c.Error(libhttp.ErrorStr(
			"freight must contain at least one commit, image, chart, or artifact",
			http.StatusBadRequest,
		))
		return
	}

	if !s.authorizeFreight(c, "create", project, "") {
		return
	}

	warehouse, err := api.GetWarehouse(
		ctx,
		s.client,
		types.NamespacedName{Namespace: project, Name: f.Origin.Name},
	)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if warehouse == nil {
		_ = c.Error(libhttp.ErrorStr(
			fmt.Sprintf("warehouse %q does not exist", f.Origin.Name),
			http.StatusBadRequest,
		))
		return
	}
	if errs := validateFreightArtifacts(f, warehouse); len(errs) > 0 {
		_ = c.Error(libhttp.Error(errs.ToAggregate(), http.StatusBadRequest))
		return
	}

	if err = s.freightStore.Create(ctx, f); err != nil {
		switch {
		case errors.Is(err, libFreight.ErrAlreadyExists):
			_ = c.Error(libhttp.ErrorStr(
				fmt.Sprintf(
					"Freight %q already exists in project %q",
					f.Name,
					project,
				),
				http.StatusConflict,
			))
		case errors.Is(err, libFreight.ErrAliasConflict):
			_ = c.Error(libhttp.ErrorStr(
				fmt.Sprintf(
					"alias %q already used by another piece of Freight in project %q",
					f.Alias,
					project,
				),
				http.StatusConflict,
			))
		default:
			_ = c.Error(fmt.Errorf("error creating Freight: %w", err))
		}
		return
	}

	if s.sender != nil {
		var actor string
		msg := "Freight created"
		if u, ok := user.InfoFromContext(ctx); ok {
			actor = api.FormatEventUserActor(u)
			msg += fmt.Sprintf(" by %q", actor)
		}
		if err = s.sender.Send(ctx, event.NewFreightCreated(msg, actor, f)); err != nil {
			logging.LoggerFromContext(ctx).Error(
				err,
				"error sending FreightCreated event",
			)
		}
	}

	c.JSON(http.StatusCreated, f)
}
