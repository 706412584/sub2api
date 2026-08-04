import { describe, expect, it } from "vitest";

import {
  createDefaultMessagesDispatchFormState,
  messagesDispatchConfigToFormState,
  messagesDispatchFormStateToConfig,
  normalizeGrokMessagesProtocolForPlatform,
  resetMessagesDispatchFormState,
} from "../groupsMessagesDispatch";

describe("groupsMessagesDispatch", () => {
  it("returns the expected default form state", () => {
    expect(createDefaultMessagesDispatchFormState()).toEqual({
      allow_messages_dispatch: false,
      opus_mapped_model: "gpt-5.4",
      sonnet_mapped_model: "gpt-5.3-codex",
      haiku_mapped_model: "gpt-5.4-mini",
      exact_model_mappings: [],
    });
  });

  it("sanitizes exact model mapping rows when converting to config", () => {
    const config = messagesDispatchFormStateToConfig({
      allow_messages_dispatch: true,
      opus_mapped_model: " gpt-5.4 ",
      sonnet_mapped_model: "gpt-5.3-codex",
      haiku_mapped_model: " gpt-5.4-mini ",
      exact_model_mappings: [
        {
          claude_model: " claude-sonnet-4-5-20250929 ",
          target_model: " gpt-5.2 ",
        },
        { claude_model: "", target_model: "gpt-5.4" },
        { claude_model: "claude-opus-4-6", target_model: " " },
      ],
    });

    expect(config).toEqual({
      opus_mapped_model: "gpt-5.4",
      sonnet_mapped_model: "gpt-5.3-codex",
      haiku_mapped_model: "gpt-5.4-mini",
      exact_model_mappings: {
        "claude-sonnet-4-5-20250929": "gpt-5.2",
      },
    });
  });

  it("hydrates form state from api config", () => {
    expect(
      messagesDispatchConfigToFormState({
        opus_mapped_model: "gpt-5.4",
        sonnet_mapped_model: "gpt-5.2",
        haiku_mapped_model: "gpt-5.4-mini",
        exact_model_mappings: {
          "claude-opus-4-6": "gpt-5.4",
          "claude-haiku-4-5-20251001": "gpt-5.4-mini",
        },
      }),
    ).toEqual({
      allow_messages_dispatch: false,
      opus_mapped_model: "gpt-5.4",
      sonnet_mapped_model: "gpt-5.2",
      haiku_mapped_model: "gpt-5.4-mini",
      exact_model_mappings: [
        {
          claude_model: "claude-haiku-4-5-20251001",
          target_model: "gpt-5.4-mini",
        },
        { claude_model: "claude-opus-4-6", target_model: "gpt-5.4" },
      ],
    });
  });

  it("resets mutable form state when platform switches away from openai", () => {
    const state = {
      allow_messages_dispatch: true,
      opus_mapped_model: "gpt-5.2",
      sonnet_mapped_model: "gpt-5.4",
      haiku_mapped_model: "gpt-5.1",
      exact_model_mappings: [
        { claude_model: "claude-opus-4-6", target_model: "gpt-5.4" },
      ],
    };

    resetMessagesDispatchFormState(state);

    expect(state).toEqual({
      allow_messages_dispatch: false,
      opus_mapped_model: "gpt-5.4",
      sonnet_mapped_model: "gpt-5.3-codex",
      haiku_mapped_model: "gpt-5.4-mini",
      exact_model_mappings: [],
    });
  });

  it("defaults Grok messages protocol to responses unless chat_completions is selected", () => {
    expect(normalizeGrokMessagesProtocolForPlatform("grok")).toBe("responses");
    expect(normalizeGrokMessagesProtocolForPlatform("grok", "responses")).toBe(
      "responses",
    );
    expect(
      normalizeGrokMessagesProtocolForPlatform("grok", "chat_completions"),
    ).toBe("chat_completions");
    expect(
      normalizeGrokMessagesProtocolForPlatform("openai", "chat_completions"),
    ).toBe("responses");
    expect(normalizeGrokMessagesProtocolForPlatform("grok", "invalid")).toBe(
      "responses",
    );
  });

});
