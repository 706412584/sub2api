package schema

import (
	"time"

	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// IMBotChat holds the pairing between one IM chat window and a bot.
type IMBotChat struct {
	ent.Schema
}

func (IMBotChat) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "im_bot_chats"},
	}
}

func (IMBotChat) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (IMBotChat) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("bot_id"),
		field.String("chat_id").
			MaxLen(255).
			NotEmpty().
			Comment("Platform-native chat identifier (TG chat id, Feishu open_id, ...)."),
		field.String("platform_user_id").
			MaxLen(255).
			Default(""),
		field.String("display_name").
			MaxLen(255).
			Default(""),
		field.String("session_uuid").
			MaxLen(36).
			NotEmpty().
			Comment("Stable sticky-session + usage-log identifier, rotated on /new."),
		field.String("model_override").
			MaxLen(200).
			Default(""),
		field.String("status").
			MaxLen(20).
			Default("active").
			Comment("active | blocked"),
		field.Time("paired_at").
			Default(time.Now),
		field.Time("last_message_at").
			Optional().
			Nillable(),
	}
}

func (IMBotChat) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("bot", IMBot.Type).
			Ref("chats").
			Field("bot_id").
			Unique().
			Required(),
		edge.To("messages", IMBotMessage.Type),
	}
}

func (IMBotChat) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("bot_id", "chat_id").Unique(),
		index.Fields("bot_id", "last_message_at"),
	}
}
