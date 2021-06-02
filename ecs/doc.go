/*
Package ecs provides interfaces to interact with AWS ECS, a container
orchestration service. Containers are not managed individually - they're managed
as logical groupings of containers called tasks.

The Client interface provides a convenience wrapper around the ECS API. A mock
implementation for testing purposes is also available in the testutil package.
*/
package ecs
