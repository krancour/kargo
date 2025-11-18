package server

import (
	"context"
	"fmt"

	"connectrpc.com/connect"

	svcv1alpha1 "github.com/akuity/kargo/api/service/v1alpha1"
)

func (s *server) GetNewServiceAccountToken(
	ctx context.Context,
	req *connect.Request[svcv1alpha1.GetNewServiceAccountTokenRequest],
) (*connect.Response[svcv1alpha1.GetNewServiceAccountTokenResponse], error) {
	project := req.Msg.Project
	if err := validateFieldNotEmpty("project", project); err != nil {
		return nil, err
	}

	if err := validateFieldNotEmpty("name", req.Msg.Name); err != nil {
		return nil, err
	}

	if err := s.validateProjectExists(ctx, project); err != nil {
		return nil, err
	}

	token, err := s.serviceAccountsDB.GetNewToken(ctx, project, req.Msg.Name)
	if err != nil {
		return nil, fmt.Errorf(
			"error getting new token for Kargo ServiceAccount %q in project %q: %w",
			req.Msg.Name, project, err,
		)
	}

	return connect.NewResponse(
		&svcv1alpha1.GetNewServiceAccountTokenResponse{
			Token: token,
		},
	), nil
}
