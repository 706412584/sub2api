<template>
  <div class="flex min-h-0 flex-1 flex-col gap-4">
    <div class="flex flex-wrap items-center gap-3">
      <div class="relative w-full sm:w-64">
        <Icon
          name="search"
          size="md"
          class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400 dark:text-gray-500"
        />
        <input
          v-model="search"
          type="text"
          class="input pl-10"
          :placeholder="t('admin.proxies.pools.searchPlaceholder')"
          @input="handleSearch"
        />
      </div>
      <div class="flex flex-1 flex-wrap items-center justify-end gap-2">
        <button type="button" class="btn btn-secondary" :disabled="loading" @click="loadPools">
          <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
        </button>
        <button type="button" class="btn btn-primary" @click="openCreate">
          <Icon name="plus" size="md" class="mr-2" />
          {{ t('admin.proxies.pools.create') }}
        </button>
      </div>
    </div>

    <div class="min-h-0 flex-1 overflow-auto rounded-lg border border-gray-200 dark:border-dark-600">
      <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-600">
        <thead class="bg-gray-50 dark:bg-dark-800">
          <tr>
            <th class="px-4 py-3 text-left font-medium text-gray-500 dark:text-gray-400">
              {{ t('admin.proxies.pools.columns.name') }}
            </th>
            <th class="px-4 py-3 text-left font-medium text-gray-500 dark:text-gray-400">
              {{ t('admin.proxies.pools.columns.protocol') }}
            </th>
            <th class="px-4 py-3 text-left font-medium text-gray-500 dark:text-gray-400">
              {{ t('admin.proxies.pools.columns.alive') }}
            </th>
            <th class="px-4 py-3 text-left font-medium text-gray-500 dark:text-gray-400">
              {{ t('admin.proxies.pools.columns.interval') }}
            </th>
            <th class="px-4 py-3 text-left font-medium text-gray-500 dark:text-gray-400">
              {{ t('admin.proxies.pools.columns.duration') }}
            </th>
            <th class="px-4 py-3 text-left font-medium text-gray-500 dark:text-gray-400">
              {{ t('admin.proxies.pools.columns.lastExtract') }}
            </th>
            <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-400">
              {{ t('admin.proxies.pools.columns.actions') }}
            </th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-100 bg-white dark:divide-dark-700 dark:bg-dark-900">
          <tr v-if="loading && pools.length === 0">
            <td colspan="7" class="px-4 py-10 text-center text-gray-400">
              {{ t('common.loading') }}
            </td>
          </tr>
          <tr v-else-if="pools.length === 0">
            <td colspan="7" class="px-4 py-10 text-center text-gray-400">
              {{ t('admin.proxies.pools.empty') }}
            </td>
          </tr>
          <tr v-for="row in pools" :key="row.id" class="hover:bg-gray-50 dark:hover:bg-dark-800/60">
            <td class="px-4 py-3">
              <div class="flex items-center gap-2">
                <span class="font-medium text-gray-900 dark:text-gray-100">{{ row.name }}</span>
                <span
                  v-if="!row.enabled"
                  class="rounded bg-amber-100 px-1.5 py-0.5 text-xs text-amber-700 dark:bg-amber-900/40 dark:text-amber-300"
                >
                  {{ t('admin.proxies.pools.disabled') }}
                </span>
              </div>
              <div class="mt-0.5 max-w-xs truncate text-xs text-gray-400" :title="row.extract_url">
                {{ row.extract_url }}
              </div>
            </td>
            <td class="px-4 py-3 uppercase text-gray-600 dark:text-gray-300">{{ row.protocol }}</td>
            <td class="px-4 py-3">
              <span :class="row.alive_count >= row.min_alive ? 'text-green-600' : 'text-orange-600'">
                {{ row.alive_count }} / {{ row.min_alive }}
              </span>
            </td>
            <td class="px-4 py-3 text-gray-600 dark:text-gray-300">{{ row.refresh_interval_sec }}s</td>
            <td class="px-4 py-3 text-gray-600 dark:text-gray-300">{{ row.ip_duration_sec }}s</td>
            <td class="px-4 py-3 text-xs text-gray-600 dark:text-gray-300">
              <div v-if="row.last_extract_at">{{ formatDateTime(row.last_extract_at) }}</div>
              <span v-if="row.last_extract_status === 'success'" class="text-green-600">
                {{ t('admin.proxies.pools.statusSuccess') }}
              </span>
              <span
                v-else-if="row.last_extract_status === 'error'"
                class="text-red-600"
                :title="row.last_extract_error"
              >
                {{ t('admin.proxies.pools.statusError') }}
              </span>
              <span v-else class="text-gray-400">{{ t('admin.proxies.pools.statusIdle') }}</span>
            </td>
            <td class="px-4 py-3">
              <div class="flex items-center justify-end gap-2">
                <button
                  type="button"
                  class="btn btn-sm btn-secondary"
                  :disabled="extractingId === row.id"
                  :title="t('admin.proxies.pools.extractNow')"
                  @click="triggerExtract(row)"
                >
                  <Icon name="refresh" size="sm" :class="extractingId === row.id ? 'animate-spin' : ''" />
                  <span class="ml-1 hidden sm:inline">
                    {{
                      extractingId === row.id
                        ? t('admin.proxies.pools.extracting')
                        : t('admin.proxies.pools.extractNow')
                    }}
                  </span>
                </button>
                <button
                  v-if="row.source_type === 'subscription'"
                  type="button"
                  class="btn btn-sm btn-secondary"
                  @click="openNodes(row)"
                >
                  <Icon name="grid" size="sm" />
                  <span class="ml-1 hidden sm:inline">{{ t('admin.proxies.pools.nodes') }}</span>
                </button>
                <button type="button" class="btn btn-sm btn-secondary" @click="managePool(row)">
                  <Icon name="cog" size="sm" />
                  <span class="ml-1 hidden sm:inline">{{ t('admin.proxies.pools.manage') }}</span>
                </button>
                <button type="button" class="btn btn-sm btn-secondary" @click="editPool(row)">
                  <Icon name="edit" size="sm" />
                </button>
                <button type="button" class="btn btn-sm btn-danger" @click="confirmDelete(row)">
                  <Icon name="trash" size="sm" />
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <Pagination
      v-if="total > pageSize"
      :total="total"
      :page="page"
      :page-size="pageSize"
      @change="handlePageChange"
    />

    <BaseDialog
      :show="showCreateModal"
      :title="t('admin.proxies.pools.create')"
      width="wide"
      @close="showCreateModal = false"
    >
      <PoolFormFields
        :model-value="createForm"
        :auth-mode-options="authModeOptions"
        :format-options="formatOptions"
        :protocol-options="protocolOptions"
        :source-type-options="sourceTypeOptions"
        :subscription-options="subscriptionOptions"
        :grok-account-options="grokAccountOptions"
        @update:model-value="Object.assign(createForm, $event)"
      />
      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" @click="showCreateModal = false">
            {{ t('common.cancel') }}
          </button>
          <button type="button" class="btn btn-primary" :disabled="saving" @click="createPool">
            {{ saving ? '...' : t('common.save') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <BaseDialog
      :show="showEditModal"
      :title="t('admin.proxies.pools.edit')"
      width="wide"
      @close="showEditModal = false"
    >
      <div class="mb-4 flex items-center gap-2">
        <input id="pool-enabled" v-model="editForm.enabled" type="checkbox" class="toggle" />
        <label for="pool-enabled" class="text-sm text-gray-700 dark:text-gray-200">
          {{ t('admin.proxies.pools.form.enabled') }}
        </label>
      </div>
      <PoolFormFields
        :model-value="editForm"
        :auth-mode-options="authModeOptions"
        :format-options="formatOptions"
        :protocol-options="protocolOptions"
        :source-type-options="sourceTypeOptions"
        :subscription-options="subscriptionOptions"
        :grok-account-options="grokAccountOptions"
        @update:model-value="Object.assign(editForm, $event)"
      />
      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" @click="showEditModal = false">
            {{ t('common.cancel') }}
          </button>
          <button type="button" class="btn btn-primary" :disabled="saving" @click="saveEdit">
            {{ saving ? '...' : t('common.save') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <ConfirmDialog
      :show="showDeleteConfirm"
      :title="t('admin.proxies.pools.delete')"
      :message="deleteMessage"
      @confirm="doDelete"
      @cancel="showDeleteConfirm = false"
    />

    <BaseDialog
      :show="showResultModal"
      :title="t('admin.proxies.pools.extractResult')"
      width="narrow"
      @close="showResultModal = false"
    >
      <div v-if="extractResult" class="space-y-2 text-sm">
        <div>
          {{ t('admin.proxies.pools.extractCreated') }}:
          <strong>{{ extractResult.created }}</strong>
        </div>
        <div>
          {{ t('admin.proxies.pools.extractFailed') }}:
          <strong>{{ extractResult.failed }}</strong>
        </div>
        <div>
          {{ t('admin.proxies.pools.extractAlive') }}:
          <strong>{{ extractResult.alive_count }}</strong>
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-primary" @click="showResultModal = false">
            {{ t('common.confirm') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <!-- Nodes preview modal (subscription pools) -->
    <BaseDialog
      :show="showNodesModal"
      :title="t('admin.proxies.pools.nodesTitle')"
      width="wide"
      @close="showNodesModal = false"
    >
      <div class="space-y-3">
        <div v-if="nodesLoading" class="py-6 text-center text-sm text-gray-400">
          {{ t('admin.proxies.pools.nodesLoading') }}
        </div>
        <div v-else-if="nodes.length === 0" class="py-6 text-center text-sm text-gray-400">
          {{ t('admin.proxies.pools.noNodes') }}
        </div>
        <template v-else>
          <div class="mb-2 flex items-center justify-between gap-2">
            <span class="text-sm text-gray-500">{{ nodes.length }} {{ t('admin.proxies.pools.columns.name') }}</span>
            <div class="flex items-center gap-2">
              <button
                type="button"
                class="btn btn-secondary btn-sm"
                :disabled="testingAllNodes || nodes.length === 0"
                @click="testAllSubscriptionNodes"
              >
                <Icon name="play" size="sm" :class="testingAllNodes ? 'animate-spin' : ''" />
                <span class="ml-1">
                  {{ testingAllNodes ? t('admin.proxies.pools.testingAll') : t('admin.proxies.pools.testAll') }}
                </span>
              </button>
              <button
                type="button"
                class="btn btn-primary btn-sm"
                :disabled="selectedNodeIds.length === 0"
                @click="addSelectedNodes"
              >
                {{ t('admin.proxies.pools.addNodes') }} ({{ selectedNodeIds.length }})
              </button>
            </div>
          </div>
          <div class="max-h-72 overflow-auto rounded border border-gray-200 dark:border-dark-600">
            <table class="min-w-full divide-y divide-gray-100 text-xs dark:divide-dark-700">
              <thead class="bg-gray-50 dark:bg-dark-800">
                <tr>
                  <th class="w-8 px-3 py-2"></th>
                  <th class="px-3 py-2 text-left font-medium text-gray-500">{{ t('admin.proxies.pools.columns.name') }}</th>
                  <th class="px-3 py-2 text-left font-medium text-gray-500">{{ t('admin.proxies.pools.columns.protocol') }}</th>
                  <th class="px-3 py-2 text-left font-medium text-gray-500">{{ t('admin.proxies.pools.address') }}</th>
                  <th class="px-3 py-2 text-left font-medium text-gray-500">{{ t('admin.proxies.pools.columns.actions') }}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-50 dark:divide-dark-800">
                <tr v-for="node in nodes" :key="node.identity" class="hover:bg-gray-50 dark:hover:bg-dark-800/60">
                  <td class="px-3 py-1.5">
                    <input type="checkbox" :value="node.identity" v-model="selectedNodeIds" class="accent-primary-500" />
                  </td>
                  <td class="px-3 py-1.5 text-gray-700 dark:text-gray-300">{{ node.name }}</td>
                  <td class="px-3 py-1.5 uppercase text-gray-500">{{ node.type }}</td>
                  <td class="px-3 py-1.5 font-mono text-gray-500">{{ node.server }}:{{ node.port }}</td>
                  <td class="px-3 py-1.5">
                    <div class="flex items-center gap-2">
                      <button
                        type="button"
                        class="btn btn-xs btn-secondary"
                        :disabled="testingNodeIdentities.has(nodeKey(node))"
                        :title="t('admin.proxies.pools.testProxy')"
                        @click="testSubscriptionNode(node)"
                      >
                        <Icon
                          name="play"
                          size="xs"
                          :class="testingNodeIdentities.has(nodeKey(node)) ? 'animate-spin' : ''"
                        />
                        <span class="ml-1">{{ t('admin.proxies.pools.testProxy') }}</span>
                      </button>
                      <span v-if="getNodeResult(node)" class="text-xs">
                        <span
                          v-if="getNodeResult(node)!.success"
                          class="text-emerald-600 dark:text-emerald-400"
                        >
                          {{ getNodeResult(node)!.latency_ms }}ms
                          <span
                            v-if="isNodeResultFresh(node)"
                            class="ml-1 text-[10px] text-gray-400"
                          >
                            ({{ t('admin.proxies.pools.testCached') }})
                          </span>
                        </span>
                        <span
                          v-else
                          class="cursor-pointer text-red-500 dark:text-red-400"
                          :title="getNodeResult(node)!.message"
                          @click.stop="openNodeFailDetail(node)"
                        >
                          {{ t('admin.proxies.pools.testFail') }}
                        </span>
                      </span>
                    </div>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </template>
      </div>
    </BaseDialog>

    <!-- Manage pool proxies -->
    <BaseDialog
      :show="showManageModal"
      :title="t('admin.proxies.pools.manageTitle')"
      width="wide"
      @close="showManageModal = false"
    >
      <div class="space-y-4">
        <!-- Entry proxy (bind target for account/group) -->
        <div
          class="rounded border border-gray-200 bg-gray-50 p-3 dark:border-dark-600 dark:bg-dark-800/60"
        >
          <div class="mb-2 flex flex-wrap items-center justify-between gap-2">
            <div class="text-sm font-medium text-gray-800 dark:text-gray-100">
              {{ t('admin.proxies.pools.entryProxy') }}
            </div>
            <span
              class="rounded px-1.5 py-0.5 text-[11px]"
              :class="
                entryProxyRunning
                  ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300'
                  : 'bg-gray-200 text-gray-600 dark:bg-dark-700 dark:text-gray-400'
              "
            >
              {{
                entryProxyRunning
                  ? t('admin.proxies.pools.entryProxyRunning')
                  : t('admin.proxies.pools.entryProxyStopped')
              }}
            </span>
          </div>
          <div class="mb-2 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.proxies.pools.entryProxyHint') }}
            <span v-if="entryProxyRecordName" class="ml-1 font-mono text-gray-600 dark:text-gray-300">
              {{ entryProxyRecordName }}
            </span>
          </div>
          <div class="flex flex-wrap items-center gap-2">
            <label class="text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.proxies.pools.entryProxyBind') }}
            </label>
            <input
              v-model="entryProxyBindAddr"
              type="text"
              class="input w-44 shrink-0 font-mono text-xs !py-1.5"
              :disabled="entryProxyBusy"
              placeholder="127.0.0.1:9900"
            />
            <button
              type="button"
              class="btn btn-sm btn-primary"
              :disabled="entryProxyBusy || !entryProxyBindAddr.trim()"
              @click="startEntryProxy"
            >
              <Icon name="play" size="sm" :class="entryProxyBusy ? 'animate-spin' : ''" />
              <span class="ml-1">{{ t('admin.proxies.pools.entryProxyStart') }}</span>
            </button>
            <button
              type="button"
              class="btn btn-sm btn-secondary"
              :disabled="entryProxyBusy || !entryProxyRunning"
              @click="stopEntryProxy"
            >
              {{ t('admin.proxies.pools.entryProxyStop') }}
            </button>
          </div>
          <div
            v-if="entryProxyRunning && entryProxyBindAddr"
            class="mt-2 font-mono text-xs text-gray-600 dark:text-gray-300"
          >
            http://{{ entryProxyBindAddr.trim() }}
          </div>
        </div>

        <div class="flex items-center justify-between gap-2">
          <div class="text-sm text-gray-600 dark:text-gray-300">
            <div>{{ t('admin.proxies.pools.manageHint') }}</div>
            <div v-if="managingGrokCheckEnabled" class="mt-1 text-xs text-amber-600 dark:text-amber-400">
              {{ t('admin.proxies.pools.grokCheckOn') }}
            </div>
          </div>
          <div class="flex gap-2">
            <button
              type="button"
              class="btn btn-sm btn-secondary"
              :disabled="testingAllProxies || poolProxies.length === 0"
              @click="testAllPoolProxies"
            >
              <Icon name="play" size="sm" :class="testingAllProxies ? 'animate-spin' : ''" />
              <span class="ml-1">
                {{ testingAllProxies ? t('admin.proxies.pools.testingAll') : t('admin.proxies.pools.testAll') }}
              </span>
            </button>
            <button type="button" class="btn btn-sm btn-secondary" @click="loadAllProxies">
              {{ t('admin.proxies.pools.loadFromIpList') }}
            </button>
          </div>
        </div>

        <!-- Pool proxies -->
        <div v-if="poolProxies.length > 0">
          <div class="mb-2 text-sm font-medium text-gray-700 dark:text-gray-200">
            {{ t('admin.proxies.pools.poolProxies') }} ({{ poolProxies.length }})
          </div>
          <div class="max-h-64 overflow-auto rounded border border-gray-200 dark:border-dark-600">
            <table class="min-w-full divide-y divide-gray-200 text-xs dark:divide-dark-600">
              <thead class="bg-gray-50 dark:bg-dark-800">
                <tr>
                  <th class="px-3 py-2 text-left font-medium text-gray-500 dark:text-gray-400">#</th>
                  <th class="px-3 py-2 text-left font-medium text-gray-500 dark:text-gray-400">{{ t('admin.proxies.pools.columns.name') }}</th>
                  <th class="px-3 py-2 text-left font-medium text-gray-500 dark:text-gray-400">{{ t('admin.proxies.pools.columns.protocol') }}</th>
                  <th class="px-3 py-2 text-left font-medium text-gray-500 dark:text-gray-400">{{ t('admin.proxies.pools.address') }}</th>
                  <th class="px-3 py-2 text-left font-medium text-gray-500 dark:text-gray-400">{{ t('admin.proxies.pools.statusSuccess') }}</th>
                  <th class="px-3 py-2 text-left font-medium text-gray-500 dark:text-gray-400">{{ t('admin.proxies.pools.grokStatus') }}</th>
                  <th class="px-3 py-2 text-right font-medium text-gray-500 dark:text-gray-400">{{ t('admin.proxies.pools.columns.actions') }}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
                <tr v-for="p in poolProxies" :key="p.id">
                  <td class="px-3 py-1.5 text-gray-400">{{ p.id }}</td>
                  <td class="px-3 py-1.5 text-gray-700 dark:text-gray-300">{{ p.name }}</td>
                  <td class="px-3 py-1.5 uppercase text-gray-500">{{ p.protocol }}</td>
                  <td class="px-3 py-1.5 text-gray-500">{{ p.host }}:{{ p.port }}</td>
                  <td class="px-3 py-1.5">
                    <span :class="p.status === 'active' ? 'text-green-600' : 'text-gray-400'">{{ p.status }}</span>
                    <span v-if="p.latency !== undefined" class="ml-1 text-xs text-gray-400">
                      {{ t('admin.proxies.pools.testLatency', { ms: p.latency }) }}
                      <span v-if="p.from_cache" class="ml-1 text-gray-400">
                        ({{ t('admin.proxies.pools.testCached') }})
                      </span>
                    </span>
                  </td>
                  <td class="px-3 py-1.5">
                    <button
                      type="button"
                      class="rounded px-1.5 py-0.5 text-[11px] cursor-pointer hover:opacity-80"
                      :class="grokStatusClass(p.grok_reasoning_status)"
                      :title="p.grok_reasoning_message || t('admin.proxies.pools.grokDetailClickHint')"
                      @click.stop="openGrokDetail(p)"
                    >
                      {{ grokStatusLabel(p.grok_reasoning_status) }}
                    </button>
                  </td>
                  <td class="px-3 py-1.5 text-right">
                    <div class="flex items-center justify-end gap-1">
                      <button
                        class="btn btn-xs btn-secondary"
                        :disabled="testingId === p.id || testingAllProxies"
                        @click="testPoolProxy(p)"
                      >
                        <Icon name="play" size="xs" :class="testingId === p.id ? 'animate-spin' : ''" />
                        {{ t('admin.proxies.pools.testProxy') }}
                      </button>
                      <button class="btn btn-xs btn-danger" @click="removePoolProxy(p)">{{ t('common.delete') }}</button>
                    </div>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>

        <!-- IP list proxies to add -->
        <div v-if="allProxies.length > 0">
          <div class="mb-2 flex items-center justify-between">
            <div class="text-sm font-medium text-gray-700 dark:text-gray-200">
              {{ t('admin.proxies.pools.ipListProxies') }} ({{ allProxies.length }})
            </div>
            <button type="button" class="btn btn-xs btn-primary" :disabled="selectedAddIds.length === 0" @click="addSelectedProxies">
              {{ t('admin.proxies.pools.addSelected') }} ({{ selectedAddIds.length }})
            </button>
          </div>
          <div class="max-h-48 overflow-auto rounded border border-gray-200 dark:border-dark-600">
            <table class="min-w-full divide-y divide-gray-200 text-xs dark:divide-dark-600">
              <thead class="bg-gray-50 dark:bg-dark-800">
                <tr>
                  <th class="w-8 px-3 py-2"></th>
                  <th class="px-3 py-2 text-left font-medium text-gray-500 dark:text-gray-400">#</th>
                  <th class="px-3 py-2 text-left font-medium text-gray-500 dark:text-gray-400">{{ t('admin.proxies.pools.columns.name') }}</th>
                  <th class="px-3 py-2 text-left font-medium text-gray-500 dark:text-gray-400">{{ t('admin.proxies.pools.columns.protocol') }}</th>
                  <th class="px-3 py-2 text-left font-medium text-gray-500 dark:text-gray-400">{{ t('admin.proxies.pools.address') }}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
                <tr v-for="p in allProxies" :key="p.id">
                  <td class="px-3 py-1.5">
                    <input type="checkbox" :value="p.id" v-model="selectedAddIds" class="accent-primary-500" />
                  </td>
                  <td class="px-3 py-1.5 text-gray-400">{{ p.id }}</td>
                  <td class="px-3 py-1.5 text-gray-700 dark:text-gray-300">{{ p.name }}</td>
                  <td class="px-3 py-1.5 uppercase text-gray-500">{{ p.protocol }}</td>
                  <td class="px-3 py-1.5 text-gray-500">{{ p.host }}:{{ p.port }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </div>
    </BaseDialog>

    <BaseDialog
      :show="showGrokDetailModal"
      :title="t('admin.proxies.pools.grokDetailTitle')"
      width="narrow"
      :z-index="80"
      @close="showGrokDetailModal = false"
    >
      <div v-if="grokDetailProxy" class="space-y-3 text-sm">
        <div class="text-gray-700 dark:text-gray-200">
          <span class="font-medium">{{ grokDetailProxy.name }}</span>
          <span class="ml-2 font-mono text-xs text-gray-500">
            {{ grokDetailProxy.host }}:{{ grokDetailProxy.port }}
          </span>
        </div>
        <div class="flex items-center gap-2">
          <span class="text-gray-500 dark:text-gray-400">{{ t('admin.proxies.pools.grokDetailStatus') }}:</span>
          <span class="rounded px-1.5 py-0.5 text-[11px]" :class="grokStatusClass(grokDetailProxy.grok_reasoning_status)">
            {{ grokStatusLabel(grokDetailProxy.grok_reasoning_status) }}
          </span>
        </div>
        <div>
          <div class="mb-1 text-gray-500 dark:text-gray-400">{{ t('admin.proxies.pools.grokDetailReason') }}</div>
          <pre
            class="max-h-56 overflow-auto whitespace-pre-wrap break-words rounded border border-gray-200 bg-gray-50 p-3 text-xs text-gray-700 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-200"
          >{{ grokDetailProxy.grok_reasoning_message || t('admin.proxies.pools.grokDetailNoReason') }}</pre>
        </div>
      </div>
      <template #footer>
        <button type="button" class="btn btn-secondary" @click="showGrokDetailModal = false">
          {{ t('common.close') }}
        </button>
      </template>
    </BaseDialog>

    <BaseDialog
      :show="showNodeFailModal"
      :title="t('admin.proxies.pools.testFail')"
      width="narrow"
      :z-index="80"
      @close="showNodeFailModal = false"
    >
      <div v-if="nodeFailDetail" class="space-y-2 text-sm">
        <div class="font-medium text-gray-800 dark:text-gray-100">{{ nodeFailDetail.name }}</div>
        <div class="font-mono text-xs text-gray-500">{{ nodeFailDetail.server }}:{{ nodeFailDetail.port }}</div>
        <pre
          class="max-h-56 overflow-auto whitespace-pre-wrap break-words rounded border border-gray-200 bg-gray-50 p-3 text-xs text-gray-700 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-200"
        >{{ nodeFailDetail.message || t('admin.proxies.pools.grokDetailNoReason') }}</pre>
      </div>
      <template #footer>
        <button type="button" class="btn btn-secondary" @click="showNodeFailModal = false">
          {{ t('common.close') }}
        </button>
      </template>
    </BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { dynamicProxyPoolsAPI } from '@/api/admin/dynamicProxyPools'
import type { DynamicProxyPool } from '@/types'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import PoolFormFields from './DynamicProxyPoolFormFields.vue'
import {
  getFreshNodeTest,
  isNodeTestFresh,
  nodeTestCacheKey,
  poolProxyTestCacheKey,
  setNodeTest,
  type NodeTestCacheEntry
} from '@/utils/nodeTestCache'

const { t } = useI18n()
const appStore = useAppStore()

const pools = ref<DynamicProxyPool[]>([])
const loading = ref(false)
const saving = ref(false)
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const search = ref('')
const searchTimer = ref<ReturnType<typeof setTimeout> | null>(null)
const extractingId = ref<number | null>(null)

const showCreateModal = ref(false)
const showEditModal = ref(false)
const showDeleteConfirm = ref(false)
const showResultModal = ref(false)
const showManageModal = ref(false)
const selectedPool = ref<DynamicProxyPool | null>(null)
const extractResult = ref<{ created: number; failed: number; alive_count: number } | null>(null)
const deleteMessage = ref('')
const poolProxies = ref<any[]>([])
const allProxies = ref<any[]>([])
const selectedAddIds = ref<number[]>([])
const managingPoolId = ref<number | null>(null)
const managingGrokCheckEnabled = ref(false)
const entryProxyBindAddr = ref('127.0.0.1:9900')
const entryProxyRunning = ref(false)
const entryProxyBusy = ref(false)
const entryProxyRecordName = ref('')
const testingId = ref<number | null>(null)
const testingAllProxies = ref(false)
const testingAllNodes = ref(false)
const showNodesModal = ref(false)
const nodesLoading = ref(false)
const nodes = ref<Array<{ identity: string; name: string; type: string; server: string; port: string }>>([])
const selectedNodeIds = ref<string[]>([])
const nodesPoolId = ref<number | null>(null)
const nodesSubscriptionId = ref<number>(0)
const nodeTestResults = reactive<Record<string, NodeTestCacheEntry>>({})
const testingNodeIdentities = reactive(new Set<string>())
const showGrokDetailModal = ref(false)
const grokDetailProxy = ref<any | null>(null)
const showNodeFailModal = ref(false)
const nodeFailDetail = ref<{ name: string; server: string; port: string; message: string } | null>(null)

type NodeRow = { identity: string; name?: string; server: string; port: string }

function nodeKey(node: NodeRow) {
  return nodeTestCacheKey(node.identity, node.server, node.port)
}

function getNodeResult(node: NodeRow): NodeTestCacheEntry | null {
  const key = nodeKey(node)
  return nodeTestResults[key] || getFreshNodeTest(key)
}

function isNodeResultFresh(node: NodeRow) {
  return isNodeTestFresh(nodeKey(node))
}

const protocolOptions = computed(() => [
  { value: 'http', label: 'HTTP' },
  { value: 'https', label: 'HTTPS' },
  { value: 'socks5', label: 'SOCKS5' },
  { value: 'socks5h', label: 'SOCKS5H' }
])

const authModeOptions = computed(() => [
  { value: 'none', label: t('admin.proxies.pools.form.authNone') },
  { value: 'fixed', label: t('admin.proxies.pools.form.authFixed') },
  { value: 'from_response', label: t('admin.proxies.pools.form.authFromResponse') }
])

const formatOptions = computed(() => [
  { value: 'txt', label: 'TXT' },
  { value: 'json', label: 'JSON' }
])

const sourceTypeOptions = [
  { value: 'extract_api', label: t('admin.proxies.pools.form.sourceExtractApi') },
  { value: 'subscription', label: t('admin.proxies.pools.form.sourceSubscription') }
]

const subscriptionOptions = ref<Array<{ value: number | null; label: string }>>([])
const subscriptionOptionsLoading = ref(false)

const grokAccountOptions = ref<Array<{ value: number | null; label: string }>>([])

async function loadSubscriptionOptions() {
  subscriptionOptionsLoading.value = true
  try {
    const { proxySubscriptionsAPI } = await import('@/api/admin')
    const res = await proxySubscriptionsAPI.list({ page_size: 200 })
    const items = Array.isArray(res?.items) ? res.items : []
    subscriptionOptions.value = items.map((s: any) => ({
      value: s.id ?? s.ID,
      label: `${s.name ?? s.Name} (${s.name_prefix ?? s.NamePrefix})`
    }))
  } catch {
    subscriptionOptions.value = []
  } finally {
    subscriptionOptionsLoading.value = false
  }
}

async function loadGrokAccountOptions() {
  try {
    const { accountsAPI } = await import('@/api/admin')
    // Prefer active first, then fall back to all Grok accounts so search isn't empty
    // when status filters exclude usable OAuth accounts.
    let items: any[] = []
    const activeRes = await accountsAPI.list(1, 200, {
      platform: 'grok',
      status: 'active',
      lite: '1'
    })
    items = Array.isArray(activeRes?.items) ? activeRes.items : []
    if (items.length === 0) {
      const allRes = await accountsAPI.list(1, 200, {
        platform: 'grok',
        lite: '1'
      })
      items = Array.isArray(allRes?.items) ? allRes.items : []
    }
    // Prefer oauth accounts, keep others as fallback
    const oauth = items.filter((a: any) => String(a.type ?? a.Type ?? '').toLowerCase() === 'oauth')
    const preferred = oauth.length > 0 ? oauth : items
    grokAccountOptions.value = preferred.map((a: any) => {
      const name = a.name ?? a.Name ?? `#${a.id ?? a.ID}`
      const type = a.type ?? a.Type ?? ''
      const status = a.status ?? a.Status ?? ''
      const parts = [type, status].filter(Boolean).join('/')
      return {
        value: a.id ?? a.ID,
        label: parts ? `${name} (${parts})` : String(name)
      }
    })
  } catch {
    grokAccountOptions.value = []
  }
}

type PoolForm = {
  name: string
  enabled?: boolean
  source_type: string
  subscription_id: number | null
  extract_url: string
  protocol: string
  auth_mode: string
  username: string
  password: string
  response_format: string
  line_separator: string
  ip_field_path: string
  port_field_path: string
  refresh_interval_sec: number
  ip_duration_sec: number
  extract_count: number
  min_alive: number
  health_check_interval_sec: number
  grok_reasoning_check_enabled: boolean
  grok_reasoning_check_account_id: number | null
  grok_reasoning_check_interval_sec: number
}

function defaultForm(): PoolForm {
  return {
    name: '',
    enabled: true,
    source_type: 'extract_api',
    subscription_id: null,
    extract_url: '',
    protocol: 'http',
    auth_mode: 'from_response',
    username: '',
    password: '',
    response_format: 'json',
    line_separator: '\\r\\n',
    ip_field_path: 'ip',
    port_field_path: 'port',
    refresh_interval_sec: 300,
    ip_duration_sec: 300,
    extract_count: 1,
    min_alive: 1,
    health_check_interval_sec: 0,
    grok_reasoning_check_enabled: false,
    grok_reasoning_check_account_id: null,
    grok_reasoning_check_interval_sec: 300
  }
}

const createForm = reactive<PoolForm>(defaultForm())
const editForm = reactive<PoolForm>(defaultForm())

function pick<T = any>(obj: Record<string, any>, ...keys: string[]): T | undefined {
  for (const k of keys) {
    if (obj[k] !== undefined && obj[k] !== null) return obj[k] as T
  }
  return undefined
}

function normalizePool(raw: Record<string, any>): DynamicProxyPool {
  return {
    id: Number(pick(raw, 'id', 'ID') ?? 0),
    name: String(pick(raw, 'name', 'Name') ?? ''),
    enabled: Boolean(pick(raw, 'enabled', 'Enabled') ?? true),
    source_type: String(pick(raw, 'source_type', 'SourceType') ?? 'extract_api'),
    subscription_id: (pick(raw, 'subscription_id', 'SubscriptionID') as number | null) ?? null,
    extract_url: String(pick(raw, 'extract_url', 'ExtractURL') ?? ''),
    protocol: String(pick(raw, 'protocol', 'Protocol') ?? 'http'),
    auth_mode: String(pick(raw, 'auth_mode', 'AuthMode') ?? 'none'),
    username: String(pick(raw, 'username', 'Username') ?? ''),
    password: String(pick(raw, 'password', 'Password') ?? ''),
    response_format: String(pick(raw, 'response_format', 'ResponseFormat') ?? 'txt'),
    line_separator: String(pick(raw, 'line_separator', 'LineSeparator') ?? '\\r\\n'),
    ip_field_path: String(pick(raw, 'ip_field_path', 'IPFieldPath') ?? ''),
    port_field_path: String(pick(raw, 'port_field_path', 'PortFieldPath') ?? ''),
    refresh_interval_sec: Number(pick(raw, 'refresh_interval_sec', 'RefreshIntervalSec') ?? 300),
    ip_duration_sec: Number(pick(raw, 'ip_duration_sec', 'IPDurationSec') ?? 300),
    extract_count: Number(pick(raw, 'extract_count', 'ExtractCount') ?? 1),
    min_alive: Number(pick(raw, 'min_alive', 'MinAlive') ?? 1),
    health_check_interval_sec: Number(pick(raw, 'health_check_interval_sec', 'HealthCheckIntervalSec') ?? 0),
    grok_reasoning_check_enabled: Boolean(pick(raw, 'grok_reasoning_check_enabled', 'GrokReasoningCheckEnabled') ?? false),
    grok_reasoning_check_account_id: (pick(raw, 'grok_reasoning_check_account_id', 'GrokReasoningCheckAccountID') as number | null) ?? null,
    grok_reasoning_check_interval_sec: Number(pick(raw, 'grok_reasoning_check_interval_sec', 'GrokReasoningCheckIntervalSec') ?? 300),
    name_prefix: String(pick(raw, 'name_prefix', 'NamePrefix') ?? ''),
    last_extract_at: (pick(raw, 'last_extract_at', 'LastExtractAt') as string | null) ?? null,
    last_extract_status: String(pick(raw, 'last_extract_status', 'LastExtractStatus') ?? ''),
    last_extract_error: String(pick(raw, 'last_extract_error', 'LastExtractError') ?? ''),
    alive_count: Number(pick(raw, 'alive_count', 'AliveCount') ?? 0),
    created_at: String(pick(raw, 'created_at', 'CreatedAt') ?? ''),
    updated_at: String(pick(raw, 'updated_at', 'UpdatedAt') ?? '')
  }
}

function formatDateTime(ts: string) {
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ts
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function handleSearch() {
  if (searchTimer.value) clearTimeout(searchTimer.value)
  searchTimer.value = setTimeout(() => {
    page.value = 1
    loadPools()
  }, 300)
}

async function loadPools() {
  loading.value = true
  try {
    const res = await dynamicProxyPoolsAPI.list({
      page: page.value,
      page_size: pageSize.value,
      search: search.value || undefined
    })
    const items = Array.isArray(res?.items) ? res.items : []
    pools.value = items.map((it) => normalizePool(it as unknown as Record<string, any>))
    total.value = Number(res?.total ?? 0)
  } catch (e: any) {
    appStore.showError(e?.message || t('admin.proxies.pools.failedToLoad'))
  } finally {
    loading.value = false
  }
}

function handlePageChange(p: number) {
  page.value = p
  loadPools()
}

function openCreate() {
  Object.assign(createForm, defaultForm())
  void loadGrokAccountOptions()
  showCreateModal.value = true
}

async function createPool() {
  if (!createForm.name.trim()) {
    appStore.showError(t('admin.proxies.pools.nameRequired'))
    return
  }
  if (createForm.source_type === 'subscription') {
    if (!createForm.subscription_id) {
      appStore.showError(t('admin.proxies.pools.form.subscription') + ' required')
      return
    }
  } else if (!createForm.extract_url.trim()) {
    appStore.showError(t('admin.proxies.pools.urlRequired'))
    return
  }
  saving.value = true
  try {
    await dynamicProxyPoolsAPI.create({ ...createForm })
    showCreateModal.value = false
    appStore.showSuccess(t('admin.proxies.pools.created'))
    await loadPools()
  } catch (e: any) {
    appStore.showError(e?.message || t('admin.proxies.pools.failedToSave'))
  } finally {
    saving.value = false
  }
}

function editPool(pool: DynamicProxyPool) {
  selectedPool.value = pool
  Object.assign(editForm, {
    name: pool.name,
    enabled: pool.enabled,
    source_type: pool.source_type,
    subscription_id: pool.subscription_id,
    extract_url: pool.extract_url,
    protocol: pool.protocol,
    auth_mode: pool.auth_mode,
    username: pool.username,
    password: '',
    response_format: pool.response_format,
    line_separator: pool.line_separator || '\\r\\n',
    ip_field_path: pool.ip_field_path,
    port_field_path: pool.port_field_path,
    refresh_interval_sec: pool.refresh_interval_sec,
    ip_duration_sec: pool.ip_duration_sec,
    extract_count: pool.extract_count,
    min_alive: pool.min_alive,
    health_check_interval_sec: pool.health_check_interval_sec ?? 0,
    grok_reasoning_check_enabled: pool.grok_reasoning_check_enabled ?? false,
    grok_reasoning_check_account_id: pool.grok_reasoning_check_account_id ?? null,
    grok_reasoning_check_interval_sec: pool.grok_reasoning_check_interval_sec ?? 300,
  })
  void loadGrokAccountOptions()
  showEditModal.value = true
}

async function saveEdit() {
  if (!selectedPool.value) return
  if (!editForm.name.trim()) {
    appStore.showError(t('admin.proxies.pools.nameRequired'))
    return
  }
  if (editForm.source_type === 'subscription') {
    if (!editForm.subscription_id) {
      appStore.showError(t('admin.proxies.pools.form.subscription') + ' required')
      return
    }
  } else if (!editForm.extract_url.trim()) {
    appStore.showError(t('admin.proxies.pools.urlRequired'))
    return
  }
  saving.value = true
  try {
    const payload: Record<string, unknown> = { ...editForm }
    if (!editForm.password) delete payload.password
    await dynamicProxyPoolsAPI.update(selectedPool.value.id, payload)
    showEditModal.value = false
    appStore.showSuccess(t('admin.proxies.pools.updated'))
    await loadPools()
  } catch (e: any) {
    appStore.showError(e?.message || t('admin.proxies.pools.failedToSave'))
  } finally {
    saving.value = false
  }
}

function confirmDelete(pool: DynamicProxyPool) {
  selectedPool.value = pool
  deleteMessage.value = t('admin.proxies.pools.deleteConfirm', { name: pool.name })
  showDeleteConfirm.value = true
}

async function doDelete() {
  if (!selectedPool.value) return
  try {
    await dynamicProxyPoolsAPI.delete(selectedPool.value.id)
    showDeleteConfirm.value = false
    appStore.showSuccess(t('admin.proxies.pools.deleted'))
    await loadPools()
  } catch (e: any) {
    appStore.showError(e?.message || t('admin.proxies.pools.failedToDelete'))
  }
}

async function triggerExtract(pool: DynamicProxyPool) {
  extractingId.value = pool.id
  try {
    const result = await dynamicProxyPoolsAPI.extract(pool.id)
    const normalized = {
      created: Number((result as any)?.created ?? (result as any)?.Created ?? 0),
      failed: Number((result as any)?.failed ?? (result as any)?.Failed ?? 0),
      alive_count: Number((result as any)?.alive_count ?? (result as any)?.AliveCount ?? 0)
    }
    extractResult.value = normalized
    showResultModal.value = true
    appStore.showSuccess(
      t('admin.proxies.pools.extractSuccess', {
        created: normalized.created,
        failed: normalized.failed,
        alive: normalized.alive_count
      })
    )
    await loadPools()
  } catch (e: any) {
    appStore.showError(e?.message || t('admin.proxies.pools.failedToExtract'))
  } finally {
    extractingId.value = null
  }
}

function restoreNodeTestResults(list: NodeRow[]) {
  Object.keys(nodeTestResults).forEach((k) => delete nodeTestResults[k])
  for (const node of list) {
    const key = nodeKey(node)
    const cached = getFreshNodeTest(key)
    if (cached) nodeTestResults[key] = cached
  }
}

function setNodeTestResult(node: NodeRow, result: Omit<NodeTestCacheEntry, 'testedAt'> & { testedAt?: number }) {
  const key = nodeKey(node)
  setNodeTest(key, result)
  const cached = getFreshNodeTest(key)
  if (cached) nodeTestResults[key] = cached
}

function openNodeFailDetail(node: NodeRow) {
  const result = getNodeResult(node)
  if (!result || result.success) return
  nodeFailDetail.value = {
    name: node.name || node.identity,
    server: node.server,
    port: String(node.port),
    message: result.message || ''
  }
  showNodeFailModal.value = true
}

async function openNodes(pool: DynamicProxyPool) {
  nodesPoolId.value = pool.id
  nodesSubscriptionId.value = Number(pool.subscription_id ?? 0) || 0
  selectedNodeIds.value = []
  testingNodeIdentities.clear()
  showNodesModal.value = true
  nodesLoading.value = true
  try {
    const res = await dynamicProxyPoolsAPI.previewNodes(pool.id)
    const items = Array.isArray(res?.nodes) ? res.nodes : []
    nodes.value = items.map((n: any) => ({
      identity: n.identity,
      name: n.name,
      type: n.type,
      server: n.server,
      port: String(n.port ?? '')
    }))
    restoreNodeTestResults(nodes.value)
  } catch (e: any) {
    appStore.showError(e?.message || 'Failed to load nodes')
    nodes.value = []
    Object.keys(nodeTestResults).forEach((k) => delete nodeTestResults[k])
  } finally {
    nodesLoading.value = false
  }
}

function grokStatusLabel(status?: string | null) {
  switch (status) {
    case 'visible':
      return t('admin.proxies.pools.grokStatusVisible')
    case 'encrypted_only':
      return t('admin.proxies.pools.grokStatusEncrypted')
    case 'no_reasoning':
      return t('admin.proxies.pools.grokStatusNone')
    case 'error':
      return t('admin.proxies.pools.grokStatusError')
    default:
      return t('admin.proxies.pools.grokStatusUnknown')
  }
}

function grokStatusClass(status?: string | null) {
  switch (status) {
    case 'visible':
      return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300'
    case 'encrypted_only':
      return 'bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300'
    case 'no_reasoning':
      return 'bg-orange-100 text-orange-700 dark:bg-orange-900/40 dark:text-orange-300'
    case 'error':
      return 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300'
    default:
      return 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-gray-400'
  }
}

function openGrokDetail(proxy: any) {
  // Always open detail so users can inspect status/message for any probe result.
  grokDetailProxy.value = {
    ...proxy,
    grok_reasoning_status: proxy?.grok_reasoning_status || '',
    grok_reasoning_message:
      proxy?.grok_reasoning_message ||
      (proxy?.grok_reasoning_status
        ? ''
        : t('admin.proxies.pools.grokStatusUnknown'))
  }
  showGrokDetailModal.value = true
}

function mapPoolProxyItem(it: any) {
  const id = it.id ?? it.ID
  const base = {
    id,
    name: it.name ?? it.Name,
    protocol: it.protocol ?? it.Protocol,
    host: it.host ?? it.Host,
    port: it.port ?? it.Port,
    status: it.status ?? it.Status,
    latency: it.latency as number | undefined,
    grok_reasoning_status: (it.grok_reasoning_status ?? it.GrokReasoningStatus ?? '') as string,
    grok_reasoning_message: (it.grok_reasoning_message ?? it.GrokReasoningMessage ?? '') as string,
    from_cache: false
  }
  // Prefer fresh client-side test cache (incl. Grok) so reopening manage shows last result.
  const cached = getFreshNodeTest(poolProxyTestCacheKey(id))
  if (cached) {
    base.status = cached.status || (cached.success ? 'active' : 'error')
    if (cached.latency_ms !== undefined) base.latency = cached.latency_ms
    if (cached.grok_reasoning_status) {
      base.grok_reasoning_status = cached.grok_reasoning_status
      base.grok_reasoning_message = cached.grok_reasoning_message || ''
    }
    base.from_cache = true
  }
  return base
}

function applyPoolProxyTestResult(
  proxy: any,
  data: {
    success?: boolean
    latency_ms?: number
    message?: string
    grok_reasoning_status?: string
    grok_reasoning_message?: string
  },
  fromCache = false
) {
  const success = !!data?.success
  proxy.latency = data?.latency_ms ?? 0
  proxy.status = success ? 'active' : 'error'
  // Always overwrite Grok fields from the latest probe/cache so stale badges clear.
  if (data?.grok_reasoning_status !== undefined && data?.grok_reasoning_status !== null) {
    proxy.grok_reasoning_status = data.grok_reasoning_status || ''
    proxy.grok_reasoning_message = data.grok_reasoning_message || ''
  }
  proxy.from_cache = fromCache
  if (!fromCache && proxy?.id) {
    setNodeTest(poolProxyTestCacheKey(proxy.id), {
      success,
      latency_ms: data?.latency_ms,
      message: data?.message || '',
      status: proxy.status,
      grok_reasoning_status: proxy.grok_reasoning_status || '',
      grok_reasoning_message: proxy.grok_reasoning_message || ''
    })
  }
  return success
}

async function testSubscriptionNode(
  node: { identity: string; server: string; port: string; name?: string },
  silent = false,
  force = false
) {
  const key = nodeKey(node)
  if (testingNodeIdentities.has(key)) return
  if (!force && isNodeTestFresh(key)) {
    const cached = getFreshNodeTest(key)
    if (cached) nodeTestResults[key] = cached
    return
  }
  testingNodeIdentities.add(key)
  try {
    const { proxySubscriptionsAPI } = await import('@/api/admin')
    const subId = nodesSubscriptionId.value > 0 ? nodesSubscriptionId.value : 0
    const res = await proxySubscriptionsAPI.testNode(subId, node.server, String(node.port))
    setNodeTestResult(node, {
      success: !!res.success,
      latency_ms: res.latency_ms,
      message: res.message || ''
    })
  } catch (e: any) {
    setNodeTestResult(node, {
      success: false,
      message: e?.message || t('admin.proxies.pools.testFail')
    })
  } finally {
    testingNodeIdentities.delete(key)
    if (!silent && !testingAllNodes.value) {
      // single-click path keeps existing UX without toast spam
    }
  }
}

async function testAllSubscriptionNodes() {
  if (testingAllNodes.value || nodes.value.length === 0) return
  testingAllNodes.value = true
  let ok = 0
  let fail = 0
  try {
    // Sequential to avoid flooding remote nodes / rate limits.
    // Fresh cache hits are reused; only stale/missing nodes are retested.
    for (const node of nodes.value) {
      await testSubscriptionNode(node, true, false)
      if (getNodeResult(node)?.success) ok++
      else fail++
    }
    appStore.showSuccess(t('admin.proxies.pools.testAllDone', { ok, fail }))
  } finally {
    testingAllNodes.value = false
  }
}

async function addSelectedNodes() {
  if (!nodesPoolId.value || selectedNodeIds.value.length === 0) return
  try {
    const res = await dynamicProxyPoolsAPI.addNodes(nodesPoolId.value, selectedNodeIds.value)
    const count = (res as any)?.created ?? 0
    appStore.showSuccess(t('admin.proxies.pools.addNodesSuccess', { count }))
    selectedNodeIds.value = []
    showNodesModal.value = false
    await loadPools()
  } catch (e: any) {
    appStore.showError(e?.message || 'Failed to add nodes')
  }
}

async function testPoolProxy(proxy: any, silent = false, force = true) {
  if (!managingPoolId.value) return false
  // Single-click always force-refreshes. Batch/test-all may reuse fresh cache.
  if (!force) {
    const cached = getFreshNodeTest(poolProxyTestCacheKey(proxy.id))
    if (cached) {
      return applyPoolProxyTestResult(proxy, cached, true)
    }
  }
  testingId.value = proxy.id
  // Drop stale "cached" badge immediately while the live probe runs.
  proxy.from_cache = false
  try {
    const res = await dynamicProxyPoolsAPI.testPoolProxy(managingPoolId.value, proxy.id)
    const data = res as any
    const success = applyPoolProxyTestResult(proxy, data, false)
    if (!silent) {
      const grokPart = data?.grok_reasoning_status
        ? ` · ${grokStatusLabel(data.grok_reasoning_status)}`
        : ''
      const detail =
        (data?.latency_ms ? ` (${data.latency_ms}ms)` : '') + grokPart
      if (success) {
        appStore.showSuccess(t('admin.proxies.pools.testSuccess') + detail)
      } else {
        appStore.showError(
          t('admin.proxies.pools.testFail') + ': ' + (data?.message || t('admin.proxies.pools.grokDetailNoReason'))
        )
      }
    }
    return success
  } catch (e: any) {
    applyPoolProxyTestResult(
      proxy,
      { success: false, message: e?.message || t('admin.proxies.pools.testFail') },
      false
    )
    if (!silent) {
      appStore.showError(e?.message || t('admin.proxies.pools.testFail'))
    }
    return false
  } finally {
    testingId.value = null
  }
}

async function testAllPoolProxies() {
  if (testingAllProxies.value || poolProxies.value.length === 0) return
  testingAllProxies.value = true
  let ok = 0
  let fail = 0
  try {
    // Reuse fresh cache for batch; only retest stale/missing entries.
    for (const proxy of poolProxies.value) {
      const success = await testPoolProxy(proxy, true, false)
      if (success) ok++
      else fail++
    }
    appStore.showSuccess(t('admin.proxies.pools.testAllDone', { ok, fail }))
  } finally {
    testingAllProxies.value = false
  }
}

/** Mirror backend sanitizePrefix so we can find pool-entry-* rows reliably. */
function sanitizePoolEntrySuffix(name: string) {
  let out = ''
  let lastDash = false
  let count = 0
  for (const ch of name || '') {
    if (count >= 20) break
    const code = ch.codePointAt(0) ?? 0
    if ((code >= 97 && code <= 122) || (code >= 48 && code <= 57)) {
      out += ch
      lastDash = false
      count++
    } else if (code >= 65 && code <= 90) {
      out += ch.toLowerCase()
      lastDash = false
      count++
    } else if (code > 0x7f) {
      out += ch
      lastDash = false
      count++
    } else if (ch === '-' || ch === ' ') {
      if (!lastDash && out.length > 0) {
        out += '-'
        lastDash = true
        count++
      }
    }
  }
  out = out.replace(/^-+|-+$/g, '')
  return out || 'pool'
}

async function refreshEntryProxyMeta(pool: DynamicProxyPool) {
  entryProxyRecordName.value = ''
  // No entry-status API; best-effort locate durable pool-entry-* row for bind addr.
  try {
    const { proxiesAPI } = await import('@/api/admin')
    const res = await proxiesAPI.list(1, 500)
    const items = Array.isArray(res?.items) ? res.items : []
    const expected = `pool-entry-${sanitizePoolEntrySuffix(pool.name || '')}`
    const entry = items.find((p: any) => {
      const name = String(p?.name ?? p?.Name ?? '')
      return name === expected
    }) as any
    const fallback =
      entry ||
      (items.find((p: any) => {
        const name = String(p?.name ?? p?.Name ?? '')
        return name.startsWith('pool-entry-') && name.includes(sanitizePoolEntrySuffix(pool.name || ''))
      }) as any)
    if (fallback) {
      const host = fallback.host ?? fallback.Host ?? '127.0.0.1'
      const port = fallback.port ?? fallback.Port
      entryProxyRecordName.value = String(fallback.name ?? fallback.Name ?? '')
      if (host && port) {
        entryProxyBindAddr.value = `${host}:${port}`
      }
    }
  } catch {
    // keep default bind
  }
}

async function startEntryProxy() {
  if (!managingPoolId.value) return
  const bind = entryProxyBindAddr.value.trim() || '127.0.0.1:9900'
  entryProxyBusy.value = true
  try {
    const res = await dynamicProxyPoolsAPI.startEntryProxy(managingPoolId.value, bind)
    const addr = (res as any)?.bind_addr || bind
    entryProxyBindAddr.value = addr
    entryProxyRunning.value = true
    if (selectedPool.value) {
      await refreshEntryProxyMeta(selectedPool.value)
    }
    appStore.showSuccess(`${t('admin.proxies.pools.entryProxyRunning')}: http://${addr}`)
  } catch (e: any) {
    appStore.showError(e?.message || t('admin.proxies.pools.entryProxyStart'))
  } finally {
    entryProxyBusy.value = false
  }
}

async function stopEntryProxy() {
  if (!managingPoolId.value) return
  entryProxyBusy.value = true
  try {
    await dynamicProxyPoolsAPI.stopEntryProxy(managingPoolId.value)
    entryProxyRunning.value = false
    appStore.showSuccess(t('admin.proxies.pools.entryProxyStopped'))
  } catch (e: any) {
    appStore.showError(e?.message || t('admin.proxies.pools.entryProxyStop'))
  } finally {
    entryProxyBusy.value = false
  }
}

async function managePool(pool: DynamicProxyPool) {
  managingPoolId.value = pool.id
  selectedPool.value = pool
  managingGrokCheckEnabled.value = !!pool.grok_reasoning_check_enabled
  poolProxies.value = []
  allProxies.value = []
  selectedAddIds.value = []
  entryProxyBindAddr.value = '127.0.0.1:9900'
  entryProxyRunning.value = false
  entryProxyRecordName.value = ''
  showManageModal.value = true
  void refreshEntryProxyMeta(pool)
  try {
    const res = await dynamicProxyPoolsAPI.listProxies(pool.id)
    const items = Array.isArray(res?.items) ? res.items : []
    managingGrokCheckEnabled.value = !!(
      (res as any)?.grok_reasoning_check_enabled ?? pool.grok_reasoning_check_enabled
    )
    // mapPoolProxyItem applies fresh client test cache (latency/Grok) when present.
    poolProxies.value = items.map(mapPoolProxyItem)
  } catch (e: any) {
    appStore.showError(e?.message || 'Failed to load pool proxies')
  }
}

async function loadAllProxies() {
  try {
    const { proxiesAPI } = await import('@/api/admin')
    const res = await proxiesAPI.list(1, 500)
    const items = Array.isArray(res?.items) ? res.items : []
    const poolIds = new Set(poolProxies.value.map((p) => p.id))
    allProxies.value = items
      .filter((p: any) => !poolIds.has(p.id ?? p.ID))
      .map((p: any) => ({
        id: p.id ?? p.ID,
        name: p.name ?? p.Name,
        protocol: p.protocol ?? p.Protocol,
        host: p.host ?? p.Host,
        port: p.port ?? p.Port,
        status: p.status ?? p.Status
      }))
  } catch (e: any) {
    appStore.showError(e?.message || 'Failed to load proxies')
  }
}

async function addSelectedProxies() {
  if (!managingPoolId.value || selectedAddIds.value.length === 0) return
  try {
    await dynamicProxyPoolsAPI.associateProxies(managingPoolId.value, selectedAddIds.value)
    selectedAddIds.value = []
    allProxies.value = []
    // Reload pool proxies
    const res = await dynamicProxyPoolsAPI.listProxies(managingPoolId.value)
    const items = Array.isArray(res?.items) ? res.items : []
    poolProxies.value = items.map(mapPoolProxyItem)
    appStore.showSuccess('Proxies added to pool')
    await loadPools()
  } catch (e: any) {
    appStore.showError(e?.message || 'Failed to add proxies')
  }
}

async function removePoolProxy(proxy: any) {
  if (!managingPoolId.value) return
  try {
    await dynamicProxyPoolsAPI.disassociateProxies(managingPoolId.value, [proxy.id])
    poolProxies.value = poolProxies.value.filter((p) => p.id !== proxy.id)
    appStore.showSuccess('Proxy removed from pool')
    await loadPools()
  } catch (e: any) {
    appStore.showError(e?.message || 'Failed to remove proxy')
  }
}

onMounted(() => {
  loadPools()
  loadSubscriptionOptions()
  loadGrokAccountOptions()
})
</script>
