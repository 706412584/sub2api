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

// IMBotMessage holds one chat message (user or assistant) for context rebuild.
type IMBotMessage struct {
	ent.Schema
}

func (IMBotMessage) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "im_bot_messages"},
	}
}

func (IMBotMessage) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (IMBotMessage) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("chat_id"),
		field.Int64("bot_id"),
		field.String("role").
			MaxLen(16).
			NotEmpty().
			Comment("user | assistant"),
		field.Text("content"),
		field.String("request_id").
			MaxLen(64).
			Default(""),
	}
}

func (IMBotMessage) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("chat", IMBotChat.Type).
			Ref("messages").
			Field("chat_id").
			Unique().
			Required(),
	}
}

func (IMBotMessage) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("chat_id", "created_at"),
		index.Fields("bot_id", "created_at"),
	}
}
