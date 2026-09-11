<template>
  <section class="rounded-lg border border-gray-200 p-4 dark:border-dark-600">
    <div class="flex items-center justify-between gap-3">
      <div>
        <h3 class="text-sm font-medium text-gray-900 dark:text-gray-100">提示词策略</h3>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
          在安全审计和路由前按规则处理该分组的文本请求。
        </p>
      </div>
      <label class="inline-flex cursor-pointer items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
        <input v-model="model.enabled" type="checkbox" class="h-4 w-4" />
        启用
      </label>
    </div>

    <div v-if="model.enabled" class="mt-4 space-y-3">
      <article
        v-for="(rule, index) in model.rules"
        :key="index"
        class="rounded-md border border-gray-200 p-3 dark:border-dark-600"
      >
        <div class="mb-3 flex items-center justify-between">
          <label class="inline-flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
            <input v-model="rule.enabled" type="checkbox" class="h-4 w-4" />
            规则 {{ index + 1 }}
          </label>
          <button type="button" class="text-sm text-red-600 hover:text-red-700" @click="removeRule(index)">
            删除
          </button>
        </div>
        <div class="grid gap-3 md:grid-cols-2">
          <label class="text-sm text-gray-700 dark:text-gray-300">
            动作
            <select v-model="rule.mode" class="input mt-1">
              <option value="replace">替换</option>
              <option value="block">阻断</option>
              <option value="prepend">前置注入</option>
              <option value="append">后置注入</option>
            </select>
          </label>
          <label class="text-sm text-gray-700 dark:text-gray-300">
            匹配类型
            <select v-model="rule.match.kind" class="input mt-1">
              <option value="literal">字面量</option>
              <option value="regex">正则表达式</option>
            </select>
          </label>
        </div>
        <label class="mt-3 block text-sm text-gray-700 dark:text-gray-300">
          匹配文本
          <textarea v-model="rule.match.value" class="input mt-1 min-h-20 w-full" />
        </label>
        <label v-if="rule.mode !== 'block'" class="mt-3 block text-sm text-gray-700 dark:text-gray-300">
          动作文本
          <textarea v-model="rule.value" class="input mt-1 min-h-20 w-full" />
        </label>
        <label class="mt-3 inline-flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
          <input v-model="rule.match.case_sensitive" type="checkbox" class="h-4 w-4" />
          区分大小写
        </label>
      </article>
      <button type="button" class="text-sm font-medium text-primary-600 hover:text-primary-700" @click="addRule">
        添加规则
      </button>
    </div>
  </section>
</template>

<script setup lang="ts">
import type { GroupPromptPolicy, GroupPromptPolicyRule } from "@/types";

const model = defineModel<GroupPromptPolicy>({ required: true });

// createRule 创建前端与后端策略结构一致的默认规则。
const createRule = (): GroupPromptPolicyRule => ({
  enabled: true,
  endpoints: ["chat_completions", "messages", "responses"],
  targets: ["system", "instructions", "message_text"],
  mode: "replace",
  match: { kind: "literal", value: "", case_sensitive: true },
  value: "",
});

// addRule 在当前分组策略尾部追加一条规则。
const addRule = () => {
  model.value.rules.push(createRule());
};

// removeRule 删除指定位置的规则。
const removeRule = (index: number) => {
  model.value.rules.splice(index, 1);
};
</script>
