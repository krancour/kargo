package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
	libFreight "github.com/akuity/kargo/pkg/freight"
	libhttp "github.com/akuity/kargo/pkg/http"
	"github.com/akuity/kargo/pkg/indexer"
)

// @id DeleteFreight
// @Summary Delete a Freight resource
// @Description Delete a Freight resource from a project's namespace by name or
// @Description alias.
// @Tags Core, Project-Level
// @Security BearerAuth
// @Param project path string true "Project name"
// @Param freight-name-or-alias path string true "Freight name or alias"
// @Success 204 "Deleted successfully"
// @Router /v1beta1/projects/{project}/freight/{freight-name-or-alias} [delete]
func (s *server) deleteFreight(c *gin.Context) {
	ctx := c.Request.Context()
	project := c.Param("project")
	nameOrAlias := c.Param("freight-name-or-alias")

	freight := s.getFreightByNameOrAliasForGin(c, project, nameOrAlias)
	if freight == nil {
		return
	}

	if !s.authorizeFreight(c, "delete", project, freight.Name) {
		return
	}

	// Refuse to delete Freight that is currently in use by any Stage
	var stages kargoapi.StageList
	if err := s.client.List(
		ctx,
		&stages,
		client.InNamespace(project),
		client.MatchingFields{indexer.StagesByFreightField: freight.Name},
	); err != nil {
		_ = c.Error(err)
		return
	}
	if len(stages.Items) > 0 {
		names := make([]string, len(stages.Items))
		for i, st := range stages.Items {
			names[i] = st.Name
		}
		_ = c.Error(libhttp.ErrorStr(
			fmt.Sprintf(
				"freight is in-use by stages (%s)",
				strings.Join(names, ", "),
			),
			http.StatusConflict,
		))
		return
	}

	if err := s.freightStore.Delete(ctx, project, freight.Name); err != nil &&
		!errors.Is(err, libFreight.ErrNotFound) {
		_ = c.Error(err)
		return
	}

	c.Status(http.StatusNoContent)
}
