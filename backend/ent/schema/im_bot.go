package schema

import (
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// IMBot holds the schema definition for the IM bot entity.
type IMBot struct {
	ent.Schema
}

func (IMBot) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "im_bots"},
	}
}

func (IMBot) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
		mixins.SoftDeleteMixin{},
	}
}

func (IMBot) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").
			MaxLen(100).
			NotEmpty(),
		field.String("platform").
			MaxLen(32).
			NotEmpty().
			Comment("telegram | feishu | dingtalk | wecom | qq | slack | wechat | whatsapp"),
		field.Text("credentials_encrypted").
			Comment("AES-256-GCM encrypted platform credential JSON"),
		field.Int64("api_key_id").
			Comment("Bound API key used for gateway calls."),
		field.String("model_override").
			MaxLen(200).
			Default(""),
		field.Text("system_prompt").
			Default(""),
		field.String("status").
			MaxLen(20).
			Default("disabled").
			Comment("enabled | disabled | error"),
		field.Int("max_concurrency").
			Default(2),
		field.Int("history_max_messages").
			Default(40),
		field.Bool("pairing_enabled").
			Default(true),
		field.Text("last_error").
			Default(""),
	}
}

func (IMBot) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("api_key", APIKey.Type).
			Ref("im_bots").
			Field("api_key_id").
			Unique().
			Required(),
		edge.To("chats", IMBotChat.Type),
	}
}

func (IMBot) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status"),
		index.Fields("api_key_id"),
	}
}
