package server

import (
	"context"
	"fmt"

	"connectrpc.com/connect"

	svcv1alpha1 "github.com/akuity/kargo/api/service/v1alpha1"
)

func (s *server) DeleteServiceAccountToken(
	ctx context.Context,
	req *connect.Request[svcv1alpha1.DeleteServiceAccountTokenRequest],
) (*connect.Response[svcv1alpha1.DeleteServiceAccountTokenResponse], error) {
	project := req.Msg.GetProject()
	if err := validateFieldNotEmpty("project", project); err != nil {
		return nil, err
	}

	name := req.Msg.GetName()
	if err := validateFieldNotEmpty("name", name); err != nil {
		return nil, err
	}

	if err := s.validateProjectExists(ctx, project); err != nil {
		return nil, err
	}

	if err := s.serviceAccountsDB.DeleteToken(ctx, project, name); err != nil {
		return nil, fmt.Errorf("delete token: %w", err)
	}
	return connect.NewResponse(
		&svcv1alpha1.DeleteServiceAccountTokenResponse{},
	), nil
}
