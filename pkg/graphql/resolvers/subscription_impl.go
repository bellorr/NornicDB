package resolvers

import (
	"context"
	"fmt"

	"github.com/orneryd/nornicdb/pkg/graphql/models"
)

// subscriptionNodeCreated subscribes to node created events.
// Returns a channel that receives events for nodes matching the label filter.
// If labels is empty, receives all node created events.
func (r *subscriptionResolver) subscriptionNodeCreated(ctx context.Context, labels []string) (<-chan *models.Node, error) {
	if r.EventBroker == nil {
		return nil, fmt.Errorf("event broker not initialized")
	}
	return r.EventBroker.SubscribeNodeCreated(ctx, labels), nil
}

// subscriptionNodeUpdated subscribes to node updated events.
// Returns a channel that receives events for nodes matching the ID or label filter.
func (r *subscriptionResolver) subscriptionNodeUpdated(ctx context.Context, id *string, labels []string) (<-chan *models.Node, error) {
	if r.EventBroker == nil {
		return nil, fmt.Errorf("event broker not initialized")
	}
	return r.EventBroker.SubscribeNodeUpdated(ctx, id, labels), nil
}

// subscriptionNodeDeleted subscribes to node deleted events.
// Returns a channel that receives node IDs for deleted nodes matching the label filter.
func (r *subscriptionResolver) subscriptionNodeDeleted(ctx context.Context, labels []string) (<-chan string, error) {
	if r.EventBroker == nil {
		return nil, fmt.Errorf("event broker not initialized")
	}
	return r.EventBroker.SubscribeNodeDeleted(ctx, labels), nil
}

// subscriptionRelationshipCreated subscribes to relationship created events.
// Returns a channel that receives events for relationships matching the type filter.
func (r *subscriptionResolver) subscriptionRelationshipCreated(ctx context.Context, types []string) (<-chan *models.Relationship, error) {
	if r.EventBroker == nil {
		return nil, fmt.Errorf("event broker not initialized")
	}
	return r.EventBroker.SubscribeRelationshipCreated(ctx, types), nil
}

// subscriptionRelationshipUpdated subscribes to relationship updated events.
// Returns a channel that receives events for relationships matching the ID or type filter.
func (r *subscriptionResolver) subscriptionRelationshipUpdated(ctx context.Context, id *string, types []string) (<-chan *models.Relationship, error) {
	if r.EventBroker == nil {
		return nil, fmt.Errorf("event broker not initialized")
	}
	return r.EventBroker.SubscribeRelationshipUpdated(ctx, id, types), nil
}

// subscriptionRelationshipDeleted subscribes to relationship deleted events.
// Returns a channel that receives relationship IDs for deleted relationships matching the type filter.
func (r *subscriptionResolver) subscriptionRelationshipDeleted(ctx context.Context, types []string) (<-chan string, error) {
	if r.EventBroker == nil {
		return nil, fmt.Errorf("event broker not initialized")
	}
	return r.EventBroker.SubscribeRelationshipDeleted(ctx, types), nil
}

// subscriptionSearchStream subscribes to streaming search results.
// This is a future enhancement and is not yet implemented.
func (r *subscriptionResolver) subscriptionSearchStream(ctx context.Context, query string, options *models.SearchOptions) (<-chan *models.SearchResult, error) {
	return nil, fmt.Errorf("searchStream subscription not yet implemented")
}
