package schema

import (
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ProxySubscription is an embedded Clash/share-link subscription source that
// syncs selected nodes into local mihomo listeners and Sub2API proxies.
type ProxySubscription struct {
	ent.Schema
}

func (ProxySubscription) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "proxy_subscriptions"},
	}
}

func (ProxySubscription) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (ProxySubscription) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").
			NotEmpty().
			MaxLen(100),
		field.Bool("enabled").
			Default(true),
		field.String("source_type").
			Default("url").
			MaxLen(20).
			Comment("url | inline"),
		field.String("subscription_url").
			Optional().
			Default("").
			MaxLen(2000).
			Sensitive().
			Comment("Remote subscription URL (https preferred)"),
		field.Text("inline_body").
			Optional().
			Default("").
			Sensitive().
			Comment("Inline subscription body (Clash YAML or share-link list / base64)"),
		field.String("name_prefix").
			Default("sidecar-a-").
			MaxLen(40).
			Comment("Owned proxy name prefix; must start with sidecar-"),
		field.String("protocol").
			Default("socks5").
			MaxLen(20).
			Comment("Protocol written to Sub2API proxies: socks5|http|https|socks5h"),
		field.String("bind_address").
			Default("127.0.0.1").
			MaxLen(64),
		field.Int("base_port").
			Default(21080),
		field.Int("max_ports").
			Default(10),
		field.Int("sync_interval_sec").
			Default(300).
			Comment("Per-source sync interval in seconds"),
		field.JSON("node_allow_contains", []string{}).
			Default([]string{}).
			Comment("Optional node-name substring allowlist; fail-closed when non-empty"),
		field.Time("last_sync_at").
			Optional().
			Nillable(),
		field.String("last_sync_status").
			Optional().
			Default("").
			MaxLen(40),
		field.Text("last_sync_error").
			Optional().
			Default(""),
		field.String("last_config_hash").
			Optional().
			Default("").
			MaxLen(64),
		field.Int("desired_count").
			Default(0),
		field.Int64("created_by").
			Optional().
			Default(0),
		field.Time("next_due_at").
			Optional().
			Nillable().
			Comment("Next scheduled sync time; nil means ASAP when enabled"),
	}
}

func (ProxySubscription) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("enabled", "next_due_at"),
		index.Fields("name_prefix"),
		index.Fields("enabled"),
	}
}
