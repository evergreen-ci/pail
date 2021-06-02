package testutil

import (
	"context"

	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/pkg/errors"
)

// MockECSClient provides a mock implementation of an ecs.Client, which can be
// used for testing purposes and introspection.
type MockECSClient struct {
}

func (c *MockECSClient) RegisterTaskDefinition(context.Context, *ecs.RegisterTaskDefinitionInput) (*ecs.RegisterContainerInstanceOutput, error) {
	return nil, errors.New("TODO: implement")
}
func (c *MockECSClient) DeregisterTaskDefinition(context.Context, *ecs.RegisterTaskDefinitionInput) (*ecs.RegisterContainerInstanceOutput, error) {
	return nil, errors.New("TODO: implement")
}
func (c *MockECSClient) RunTask(context.Context, *ecs.RegisterTaskDefinitionInput) (*ecs.RegisterContainerInstanceOutput, error) {
	return nil, errors.New("TODO: implement")
}
