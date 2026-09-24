package server

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	libFreight "github.com/akuity/kargo/pkg/freight"
	libhttp "github.com/akuity/kargo/pkg/http"
)

// @id PatchFreightAlias
// @Summary Patch a Freight resource's alias
// @Description Patch a Freight resource's human-friendly alias.
// @Tags Core, Project-Level
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param project path string true "Project name"
// @Param freight-name-or-alias path string true "Freight name or alias"
// @Param newAlias query string true "New alias"
// @Success 200 "Success"
// @Router /v1beta1/projects/{project}/freight/{freight-name-or-alias}/alias [patch]
func (s *server) patchFreightAliasHandler(c *gin.Context) {
	ctx := c.Request.Context()
	project := c.Param("project")
	nameOrAlias := c.Param("freight-name-or-alias")
	newAlias := c.Query("newAlias")

	if newAlias == "" {
		_ = c.Error(libhttp.ErrorStr(
			"newAlias query parameter is required",
			http.StatusBadRequest,
		))
		return
	}

	freight := s.getFreightByNameOrAliasForGin(c, project, nameOrAlias)
	if freight == nil {
		return
	}

	if !s.authorizeFreight(c, "patch", project, freight.Name) {
		return
	}

	if _, err := s.freightStore.UpdateAlias(ctx, project, freight.Name, newAlias); err != nil {
		if errors.Is(err, libFreight.ErrAliasConflict) {
			_ = c.Error(libhttp.ErrorStr(
				fmt.Sprintf(
					"alias %q already used by another piece of Freight in namespace %q",
					newAlias,
					project,
				),
				http.StatusConflict,
			))
			return
		}
		_ = c.Error(err)
		return
	}

	c.Status(http.StatusOK)
}
