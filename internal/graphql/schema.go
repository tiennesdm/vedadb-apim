// Package graphql provides the GraphQL schema, resolvers, and HTTP server for the VedaDB API Manager.
package graphql

import (
	"fmt"

	"github.com/graphql-go/graphql"
)

// ---- Scalar Types ----

// JSONScalar is a custom scalar for arbitrary JSON values.
var JSONScalar = graphql.NewScalar(graphql.ScalarConfig{
	Name:        "JSON",
	Description: "The JSON scalar type represents arbitrary JSON values.",
	Serialize: func(value interface{}) interface{} {
		switch v := value.(type) {
		case map[string]interface{}:
			return v
		case []interface{}:
			return v
		default:
			return value
		}
	},
	ParseValue: func(value interface{}) interface{} {
		return value
	},
	ParseLiteral: func(valueAST interface{}) interface{} {
		return valueAST
	},
})

// TimestampScalar is a custom scalar for ISO 8601 timestamps.
var TimestampScalar = graphql.NewScalar(graphql.ScalarConfig{
	Name:        "Timestamp",
	Description: "ISO 8601 formatted timestamp string.",
	Serialize: func(value interface{}) interface{} {
		switch v := value.(type) {
		case string:
			return v
		default:
			return fmt.Sprintf("%v", value)
		}
	},
	ParseValue: func(value interface{}) interface{} {
		return value
	},
	ParseLiteral: func(valueAST interface{}) interface{} {
		return valueAST
	},
})

// ---- Object Types ----

// UserType is the GraphQL type for User.
var UserType = graphql.NewObject(graphql.ObjectConfig{
	Name: "User",
	Fields: graphql.Fields{
		"id": &graphql.Field{Type: graphql.NewNonNull(graphql.ID)},
		"username": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
		"email": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
		"role": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
		"status": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
	},
})

// APIResourceType is the GraphQL type for APIResource.
var APIResourceType = graphql.NewObject(graphql.ObjectConfig{
	Name: "APIResource",
	Fields: graphql.Fields{
		"id": &graphql.Field{Type: graphql.NewNonNull(graphql.ID)},
		"method": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
		"path": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
		"description": &graphql.Field{Type: graphql.String},
		"authRequired": &graphql.Field{Type: graphql.NewNonNull(graphql.Boolean)},
	},
})

// APIVersionType is the GraphQL type for APIVersion.
var APIVersionType = graphql.NewObject(graphql.ObjectConfig{
	Name: "APIVersion",
	Fields: graphql.Fields{
		"id": &graphql.Field{Type: graphql.NewNonNull(graphql.ID)},
		"version": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
		"status": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
		"createdAt": &graphql.Field{Type: TimestampScalar},
	},
})

// ApplicationKeyType is the GraphQL type for ApplicationKey.
var ApplicationKeyType = graphql.NewObject(graphql.ObjectConfig{
	Name: "ApplicationKey",
	Fields: graphql.Fields{
		"id": &graphql.Field{Type: graphql.NewNonNull(graphql.ID)},
		"keyType": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
		"token": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
		"validity": &graphql.Field{Type: graphql.NewNonNull(graphql.Int)},
		"createdAt": &graphql.Field{Type: TimestampScalar},
	},
})

// ApplicationType is the GraphQL type for Application.
var ApplicationType = graphql.NewObject(graphql.ObjectConfig{
	Name: "Application",
	Fields: graphql.Fields{
		"id": &graphql.Field{Type: graphql.NewNonNull(graphql.ID)},
		"name": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
		"description": &graphql.Field{Type: graphql.String},
		"owner": &graphql.Field{
			Type: UserType,
			// Resolved via field resolver
		},
		"tier": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
		"status": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
		"keys": &graphql.Field{
			Type: graphql.NewList(graphql.NewNonNull(ApplicationKeyType)),
		},
		"createdAt": &graphql.Field{Type: TimestampScalar},
	},
})

// SubscriptionType is the GraphQL type for Subscription.
var SubscriptionType = graphql.NewObject(graphql.ObjectConfig{
	Name: "Subscription",
	Fields: graphql.Fields{
		"id": &graphql.Field{Type: graphql.NewNonNull(graphql.ID)},
		"api": &graphql.Field{
			Type: graphql.NewNonNull(APIType),
		},
		"application": &graphql.Field{
			Type: graphql.NewNonNull(ApplicationType),
		},
		"tier": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
		"status": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
		"createdAt": &graphql.Field{Type: TimestampScalar},
	},
})

// APIType is the GraphQL type for API.
// Defined lazily to handle circular references with SubscriptionType.
var APIType *graphql.Object

func init() {
	APIType = graphql.NewObject(graphql.ObjectConfig{
		Name: "API",
		Fields: graphql.Fields{
			"id": &graphql.Field{Type: graphql.NewNonNull(graphql.ID)},
			"name": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
			"description": &graphql.Field{Type: graphql.String},
			"context": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
			"version": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
			"endpoint": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
			"authType": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
			"status": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
			"provider": &graphql.Field{Type: graphql.String},
			"tags": &graphql.Field{Type: graphql.NewList(graphql.NewNonNull(graphql.String))},
			"rating": &graphql.Field{Type: graphql.Float},
			"resources": &graphql.Field{
				Type: graphql.NewList(graphql.NewNonNull(APIResourceType)),
			},
			"versions": &graphql.Field{
				Type: graphql.NewList(graphql.NewNonNull(APIVersionType)),
			},
			"subscriptions": &graphql.Field{
				Type: graphql.NewList(graphql.NewNonNull(SubscriptionType)),
			},
			"createdAt": &graphql.Field{Type: TimestampScalar},
			"updatedAt": &graphql.Field{Type: TimestampScalar},
		},
	})
}

// AnalyticsSummaryType is the GraphQL type for AnalyticsSummary.
var AnalyticsSummaryType = graphql.NewObject(graphql.ObjectConfig{
	Name: "AnalyticsSummary",
	Fields: graphql.Fields{
		"apiId": &graphql.Field{Type: graphql.NewNonNull(graphql.ID)},
		"apiName": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
		"requestCount": &graphql.Field{Type: graphql.NewNonNull(graphql.Int)},
		"errorCount": &graphql.Field{Type: graphql.NewNonNull(graphql.Int)},
		"avgLatency": &graphql.Field{Type: graphql.NewNonNull(graphql.Int)},
		"p95Latency": &graphql.Field{Type: graphql.NewNonNull(graphql.Int)},
		"p99Latency": &graphql.Field{Type: graphql.NewNonNull(graphql.Int)},
		"uniqueUsers": &graphql.Field{Type: graphql.NewNonNull(graphql.Int)},
	},
})

// ThrottlePolicyType is the GraphQL type for ThrottlePolicy.
var ThrottlePolicyType = graphql.NewObject(graphql.ObjectConfig{
	Name: "ThrottlePolicy",
	Fields: graphql.Fields{
		"id": &graphql.Field{Type: graphql.NewNonNull(graphql.ID)},
		"name": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
		"description": &graphql.Field{Type: graphql.String},
		"quotaType": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
		"requestCount": &graphql.Field{Type: graphql.NewNonNull(graphql.Int)},
		"timeUnit": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
		"rateLimitCount": &graphql.Field{Type: graphql.NewNonNull(graphql.Int)},
		"rateLimitUnit": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
		"isDeployed": &graphql.Field{Type: graphql.NewNonNull(graphql.Boolean)},
		"createdAt": &graphql.Field{Type: TimestampScalar},
		"updatedAt": &graphql.Field{Type: TimestampScalar},
	},
})

// AuditLogEntryType is the GraphQL type for AuditLogEntry.
var AuditLogEntryType = graphql.NewObject(graphql.ObjectConfig{
	Name: "AuditLogEntry",
	Fields: graphql.Fields{
		"id": &graphql.Field{Type: graphql.NewNonNull(graphql.ID)},
		"action": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
		"resourceType": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
		"resourceId": &graphql.Field{Type: graphql.NewNonNull(graphql.ID)},
		"user": &graphql.Field{Type: UserType},
		"ipAddress": &graphql.Field{Type: graphql.String},
		"details": &graphql.Field{Type: JSONScalar},
		"createdAt": &graphql.Field{Type: TimestampScalar},
	},
})

// WebhookType is the GraphQL type for Webhook.
var WebhookType = graphql.NewObject(graphql.ObjectConfig{
	Name: "Webhook",
	Fields: graphql.Fields{
		"id": &graphql.Field{Type: graphql.NewNonNull(graphql.ID)},
		"name": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
		"callbackUrl": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
		"eventTypes": &graphql.Field{Type: graphql.NewList(graphql.NewNonNull(graphql.String))},
		"active": &graphql.Field{Type: graphql.NewNonNull(graphql.Boolean)},
		"createdAt": &graphql.Field{Type: TimestampScalar},
		"updatedAt": &graphql.Field{Type: TimestampScalar},
	},
})

// ---- Input Types ----

// CreateAPIInputType is the GraphQL input type for creating an API.
var CreateAPIInputType = graphql.NewInputObject(graphql.InputObjectConfig{
	Name: "CreateAPIInput",
	Fields: graphql.InputObjectConfigFieldMap{
		"name": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.String)},
		"description": &graphql.InputObjectFieldConfig{Type: graphql.String},
		"context": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.String)},
		"version": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.String)},
		"endpoint": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.String)},
		"authType": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.String)},
		"provider": &graphql.InputObjectFieldConfig{Type: graphql.String},
		"tags": &graphql.InputObjectFieldConfig{Type: graphql.NewList(graphql.NewNonNull(graphql.String))},
	},
})

// UpdateAPIInputType is the GraphQL input type for updating an API.
var UpdateAPIInputType = graphql.NewInputObject(graphql.InputObjectConfig{
	Name: "UpdateAPIInput",
	Fields: graphql.InputObjectConfigFieldMap{
		"name": &graphql.InputObjectFieldConfig{Type: graphql.String},
		"description": &graphql.InputObjectFieldConfig{Type: graphql.String},
		"context": &graphql.InputObjectFieldConfig{Type: graphql.String},
		"version": &graphql.InputObjectFieldConfig{Type: graphql.String},
		"endpoint": &graphql.InputObjectFieldConfig{Type: graphql.String},
		"authType": &graphql.InputObjectFieldConfig{Type: graphql.String},
		"provider": &graphql.InputObjectFieldConfig{Type: graphql.String},
		"tags": &graphql.InputObjectFieldConfig{Type: graphql.NewList(graphql.NewNonNull(graphql.String))},
		"status": &graphql.InputObjectFieldConfig{Type: graphql.String},
	},
})

// CreateAppInputType is the GraphQL input type for creating an Application.
var CreateAppInputType = graphql.NewInputObject(graphql.InputObjectConfig{
	Name: "CreateAppInput",
	Fields: graphql.InputObjectConfigFieldMap{
		"name": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.String)},
		"description": &graphql.InputObjectFieldConfig{Type: graphql.String},
		"tier": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.String)},
	},
})

// CreateUserInputType is the GraphQL input type for creating a User.
var CreateUserInputType = graphql.NewInputObject(graphql.InputObjectConfig{
	Name: "CreateUserInput",
	Fields: graphql.InputObjectConfigFieldMap{
		"username": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.String)},
		"email": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.String)},
		"role": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.String)},
	},
})

// CreatePolicyInputType is the GraphQL input type for creating a ThrottlePolicy.
var CreatePolicyInputType = graphql.NewInputObject(graphql.InputObjectConfig{
	Name: "CreatePolicyInput",
	Fields: graphql.InputObjectConfigFieldMap{
		"name": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.String)},
		"description": &graphql.InputObjectFieldConfig{Type: graphql.String},
		"quotaType": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.String)},
		"requestCount": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.Int)},
		"timeUnit": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.String)},
		"rateLimitCount": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.Int)},
		"rateLimitUnit": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.String)},
	},
})

// CreateWebhookInputType is the GraphQL input type for creating a Webhook.
var CreateWebhookInputType = graphql.NewInputObject(graphql.InputObjectConfig{
	Name: "CreateWebhookInput",
	Fields: graphql.InputObjectConfigFieldMap{
		"apiId": &graphql.InputObjectFieldConfig{Type: graphql.ID},
		"name": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.String)},
		"callbackUrl": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.String)},
		"secret": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.String)},
		"eventTypes": &graphql.InputObjectFieldConfig{Type: graphql.NewList(graphql.NewNonNull(graphql.String))},
	},
})

// buildSchema creates and returns the compiled GraphQL schema.
func buildSchema(r *Resolver) (graphql.Schema, error) {
	// ---- Root Query ----
	queryFields := graphql.Fields{
		"apis": &graphql.Field{
			Type: graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(APIType))),
			Args: graphql.FieldConfigArgument{
				"status": &graphql.ArgumentConfig{Type: graphql.String},
				"limit":  &graphql.ArgumentConfig{Type: graphql.Int, DefaultValue: 50},
				"offset": &graphql.ArgumentConfig{Type: graphql.Int, DefaultValue: 0},
			},
			Resolve: r.ResolveAPIs,
		},
		"api": &graphql.Field{
			Type: APIType,
			Args: graphql.FieldConfigArgument{
				"id": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.ID)},
			},
			Resolve: r.ResolveAPI,
		},
		"publishedAPIs": &graphql.Field{
			Type: graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(APIType))),
			Args: graphql.FieldConfigArgument{
				"limit":  &graphql.ArgumentConfig{Type: graphql.Int, DefaultValue: 50},
				"offset": &graphql.ArgumentConfig{Type: graphql.Int, DefaultValue: 0},
			},
			Resolve: r.ResolvePublishedAPIs,
		},
		"searchAPIs": &graphql.Field{
			Type: graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(APIType))),
			Args: graphql.FieldConfigArgument{
				"query":  &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
				"limit":  &graphql.ArgumentConfig{Type: graphql.Int, DefaultValue: 50},
				"offset": &graphql.ArgumentConfig{Type: graphql.Int, DefaultValue: 0},
			},
			Resolve: r.ResolveSearchAPIs,
		},
		"applications": &graphql.Field{
			Type: graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(ApplicationType))),
			Args: graphql.FieldConfigArgument{
				"limit":  &graphql.ArgumentConfig{Type: graphql.Int, DefaultValue: 50},
				"offset": &graphql.ArgumentConfig{Type: graphql.Int, DefaultValue: 0},
			},
			Resolve: r.ResolveApplications,
		},
		"application": &graphql.Field{
			Type: ApplicationType,
			Args: graphql.FieldConfigArgument{
				"id": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.ID)},
			},
			Resolve: r.ResolveApplication,
		},
		"subscriptions": &graphql.Field{
			Type: graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(SubscriptionType))),
			Args: graphql.FieldConfigArgument{
				"apiId": &graphql.ArgumentConfig{Type: graphql.ID},
				"appId": &graphql.ArgumentConfig{Type: graphql.ID},
			},
			Resolve: r.ResolveSubscriptions,
		},
		"users": &graphql.Field{
			Type: graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(UserType))),
			Args: graphql.FieldConfigArgument{
				"limit":  &graphql.ArgumentConfig{Type: graphql.Int, DefaultValue: 50},
				"offset": &graphql.ArgumentConfig{Type: graphql.Int, DefaultValue: 0},
			},
			Resolve: r.ResolveUsers,
		},
		"user": &graphql.Field{
			Type: UserType,
			Args: graphql.FieldConfigArgument{
				"id": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.ID)},
			},
			Resolve: r.ResolveUser,
		},
		"analytics": &graphql.Field{
			Type: AnalyticsSummaryType,
			Args: graphql.FieldConfigArgument{
				"apiId":  &graphql.ArgumentConfig{Type: graphql.ID},
				"period": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
			},
			Resolve: r.ResolveAnalytics,
		},
		"topAPIs": &graphql.Field{
			Type: graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(AnalyticsSummaryType))),
			Args: graphql.FieldConfigArgument{
				"limit": &graphql.ArgumentConfig{Type: graphql.Int, DefaultValue: 10},
			},
			Resolve: r.ResolveTopAPIs,
		},
		"throttlePolicies": &graphql.Field{
			Type: graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(ThrottlePolicyType))),
			Resolve: r.ResolveThrottlePolicies,
		},
		"auditLogs": &graphql.Field{
			Type: graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(AuditLogEntryType))),
			Args: graphql.FieldConfigArgument{
				"limit":  &graphql.ArgumentConfig{Type: graphql.Int, DefaultValue: 50},
				"offset": &graphql.ArgumentConfig{Type: graphql.Int, DefaultValue: 0},
			},
			Resolve: r.ResolveAuditLogs,
		},
	}

	// ---- Root Mutation ----
	mutationFields := graphql.Fields{
		"createAPI": &graphql.Field{
			Type: graphql.NewNonNull(APIType),
			Args: graphql.FieldConfigArgument{
				"input": &graphql.ArgumentConfig{Type: graphql.NewNonNull(CreateAPIInputType)},
			},
			Resolve: r.ResolveCreateAPI,
		},
		"updateAPI": &graphql.Field{
			Type: APIType,
			Args: graphql.FieldConfigArgument{
				"id":    &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.ID)},
				"input": &graphql.ArgumentConfig{Type: graphql.NewNonNull(UpdateAPIInputType)},
			},
			Resolve: r.ResolveUpdateAPI,
		},
		"deleteAPI": &graphql.Field{
			Type: graphql.Boolean,
			Args: graphql.FieldConfigArgument{
				"id": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.ID)},
			},
			Resolve: r.ResolveDeleteAPI,
		},
		"publishAPI": &graphql.Field{
			Type: APIType,
			Args: graphql.FieldConfigArgument{
				"id": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.ID)},
			},
			Resolve: r.ResolvePublishAPI,
		},
		"createApplication": &graphql.Field{
			Type: graphql.NewNonNull(ApplicationType),
			Args: graphql.FieldConfigArgument{
				"input": &graphql.ArgumentConfig{Type: graphql.NewNonNull(CreateAppInputType)},
			},
			Resolve: r.ResolveCreateApplication,
		},
		"subscribe": &graphql.Field{
			Type: graphql.NewNonNull(SubscriptionType),
			Args: graphql.FieldConfigArgument{
				"apiId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.ID)},
				"appId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.ID)},
				"tier":  &graphql.ArgumentConfig{Type: graphql.String, DefaultValue: "Unlimited"},
			},
			Resolve: r.ResolveSubscribe,
		},
		"unsubscribe": &graphql.Field{
			Type: graphql.Boolean,
			Args: graphql.FieldConfigArgument{
				"id": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.ID)},
			},
			Resolve: r.ResolveUnsubscribe,
		},
		"createUser": &graphql.Field{
			Type: graphql.NewNonNull(UserType),
			Args: graphql.FieldConfigArgument{
				"input": &graphql.ArgumentConfig{Type: graphql.NewNonNull(CreateUserInputType)},
			},
			Resolve: r.ResolveCreateUser,
		},
		"createThrottlePolicy": &graphql.Field{
			Type: graphql.NewNonNull(ThrottlePolicyType),
			Args: graphql.FieldConfigArgument{
				"input": &graphql.ArgumentConfig{Type: graphql.NewNonNull(CreatePolicyInputType)},
			},
			Resolve: r.ResolveCreateThrottlePolicy,
		},
		"createWebhook": &graphql.Field{
			Type: graphql.NewNonNull(WebhookType),
			Args: graphql.FieldConfigArgument{
				"input": &graphql.ArgumentConfig{Type: graphql.NewNonNull(CreateWebhookInputType)},
			},
			Resolve: r.ResolveCreateWebhook,
		},
		"deleteWebhook": &graphql.Field{
			Type: graphql.Boolean,
			Args: graphql.FieldConfigArgument{
				"id": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.ID)},
			},
			Resolve: r.ResolveDeleteWebhook,
		},
	}

	rootQuery := graphql.NewObject(graphql.ObjectConfig{
		Name:   "Query",
		Fields: queryFields,
	})

	rootMutation := graphql.NewObject(graphql.ObjectConfig{
		Name:   "Mutation",
		Fields: mutationFields,
	})

	schemaConfig := graphql.SchemaConfig{
		Query:    rootQuery,
		Mutation: rootMutation,
		Types: []graphql.Type{
			APIType,
			APIResourceType,
			APIVersionType,
			ApplicationType,
			ApplicationKeyType,
			UserType,
			SubscriptionType,
			AnalyticsSummaryType,
			ThrottlePolicyType,
			AuditLogEntryType,
			WebhookType,
			JSONScalar,
			TimestampScalar,
		},
	}

	return graphql.NewSchema(schemaConfig)
}
