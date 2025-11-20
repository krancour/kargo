package server

import (
	"context"
	"fmt"

	"connectrpc.com/connect"

	svcv1alpha1 "github.com/akuity/kargo/api/service/v1alpha1"
)

func (s *server) CreateServiceAccountToken(
	ctx context.Context,
	req *connect.Request[svcv1alpha1.CreateServiceAccountTokenRequest],
) (*connect.Response[svcv1alpha1.CreateServiceAccountTokenResponse], error) {
	project := req.Msg.Project
	if err := validateFieldNotEmpty("project", project); err != nil {
		return nil, err
	}

	serviceAccountName := req.Msg.ServiceAccountName
	if err := validateFieldNotEmpty(
		"service_account_name",
		serviceAccountName,
	); err != nil {
		return nil, err
	}

	name := req.Msg.Name
	if err := validateFieldNotEmpty("name", name); err != nil {
		return nil, err
	}

	if err := s.validateProjectExists(ctx, project); err != nil {
		return nil, err
	}

	tokenSecret, err := s.serviceAccountsDB.CreateToken(
		ctx,
		project,
		serviceAccountName,
		name,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"error creating new token %q for Kargo ServiceAccount %q in project %q: %w",
			name, serviceAccountName, project, err,
		)
	}

	return connect.NewResponse(
		&svcv1alpha1.CreateServiceAccountTokenResponse{
			TokenSecret: tokenSecret,
		},
	), nil
}
