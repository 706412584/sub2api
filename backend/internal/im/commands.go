package im

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// CommandResult tells the runtime what happened with a slash command.
type CommandResult struct {
	// Handled is true when the message was a command and is fully processed
	// (no forwarding happens).
	Handled bool
	// Reply is an optional notice to send back.
	Reply string
}

// CommandDeps bundles what command handlers may touch.
type CommandDeps struct {
	Repo service.IMBotRepository
	// ChatRuntime exposes /stop cancellation for the current chat turn.
	StopCurrentTurn func(chatID string)
	// EffectiveModel resolves the chat's current model label.
	EffectiveModel func(chat *service.IMBotChat) string
	// MaxHistoryBytes caps the context window.
	MaxHistoryBytes int
}

// commandAliases maps user-facing commands (with /) to canonical names.
// Plain-chat bot commands (cc-haha subset): /new /clear /status /stop
// /model /help.
func HandleCommand(ctx context.Context, text string, bot *service.IMBot, chat *service.IMBotChat, deps CommandDeps) (CommandResult, error) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "/") {
		return CommandResult{}, nil
	}
	cmd, args, _ := strings.Cut(trimmed[1:], " ")
	cmd = strings.ToLower(cmd)
	args = strings.TrimSpace(args)

	switch cmd {
	case "new", "start":
		return resetSession(ctx, bot, chat, deps)
	case "clear":
		// /clear keeps the session binding but wipes stored context.
		if err := deps.Repo.DeleteChatMessages(ctx, chat.ID); err != nil {
			return CommandResult{}, err
		}
		return CommandResult{Handled: true, Reply: "✅ 已清空上下文（会话保持绑定）"}, nil
	case "status":
		msgs, err := deps.Repo.ListRecentMessages(ctx, chat.ID, 1)
		if err != nil {
			return CommandResult{}, err
		}
		lastAt := "—"
		if len(msgs) > 0 {
			lastAt = msgs[0].CreatedAt.Local().Format("01-02 15:04")
		}
		model := chat.ModelOverride
		if model == "" {
			model = bot.ModelOverride
		}
		if model == "" {
			model = "(分组默认)"
		}
		reply := fmt.Sprintf("机器人：%s\n平台：%s\n模型：%s\n最近消息：%s", bot.Name, bot.Platform, model, lastAt)
		return CommandResult{Handled: true, Reply: reply}, nil
	case "stop":
		deps.StopCurrentTurn(chat.ChatID)
		return CommandResult{Handled: true, Reply: "⏹ 已请求停止当前生成"}, nil
	case "model":
		if args == "" {
			cur := deps.EffectiveModel(chat)
			if cur == "" {
				cur = "(分组默认)"
			}
			return CommandResult{Handled: true, Reply: fmt.Sprintf("当前模型：%s\n用法：/model <模型名>（留空清除）", cur)}, nil
		}
		fields := service.IMChatUpdateFields{ModelOverride: &args}
		if err := deps.Repo.UpdateChat(ctx, chat.ID, fields); err != nil {
			return CommandResult{}, err
		}
		chat.ModelOverride = args
		return CommandResult{Handled: true, Reply: "✅ 本会话模型已切换：" + args}, nil
	case "help":
		return CommandResult{Handled: true, Reply: helpText()}, nil
	default:
		// Unknown slash command: treat as plain text so the model can react
		// naturally (avoids hard errors on platform-native commands like
		// Telegram's /start which some clients auto-send).
		if cmd == "start" {
			return resetSession(ctx, bot, chat, deps)
		}
		return CommandResult{}, nil
	}
}

// resetSession implements /new: wipe stored context AND rotate the session
// uuid so sticky scheduling and prompt caches don't leak the old context.
func resetSession(ctx context.Context, bot *service.IMBot, chat *service.IMBotChat, deps CommandDeps) (CommandResult, error) {
	if err := deps.Repo.DeleteChatMessages(ctx, chat.ID); err != nil {
		return CommandResult{}, err
	}
	newUUID, err := deps.Repo.RotateChatSessionUUID(ctx, chat.ID)
	if err != nil {
		return CommandResult{}, err
	}
	chat.SessionUUID = newUUID
	_ = time.Now()
	return CommandResult{Handled: true, Reply: "✨ 新会话已开始（上下文与账号绑定已重置）"}, nil
}

func helpText() string {
	return "可用命令：\n" +
		"/new — 新会话（清空上下文并重置绑定）\n" +
		"/clear — 清空上下文（保持绑定）\n" +
		"/status — 查看机器人状态\n" +
		"/stop — 停止当前生成\n" +
		"/model [名称] — 查看/切换本会话模型\n" +
		"/help — 显示本帮助\n" +
		"直接发送文字即可聊天。"
}
