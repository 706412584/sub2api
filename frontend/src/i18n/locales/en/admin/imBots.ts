export const imBotsEn = {
  title: 'IM Bots',
  description: 'Bridge private IM chats (Telegram / Feishu / ...) to the gateway — chat with a model-bound bot from your phone',
  create: 'Create Bot',
  loadFailed: 'Failed to load bots',
  saved: 'Saved',
  saveFailed: 'Save failed',
  deleteConfirm: 'Delete bot "{name}"? Its chat history will be removed too.',
  deleteFailed: 'Delete failed',
  toggleFailed: 'Failed to toggle',
  testOk: 'Connection test passed',
  testFailed: 'Connection test failed',
  empty: 'No bots yet — create one to get started',
  comingSoon: 'coming soon',
  groupDefault: 'group default',
  status: {
    enabled: 'running',
    disabled: 'disabled',
    error: 'error'
  },
  actions: {
    test: 'Test',
    enable: 'Enable',
    disable: 'Disable',
    pairCode: 'Pair Code',
    chats: 'Chats'
  },
  statusLabel: 'Status',
  fields: {
    name: 'Name',
    platform: 'Platform',
    apiKey: 'Bound API Key',
    model: 'Model override',
    systemPrompt: 'System prompt',
    maxConcurrency: 'Max concurrency',
    historyMax: 'Context messages',
    pairingEnabled: 'Require pairing code (deny strangers by default)'
  },
  form: {
    createTitle: 'Create IM Bot',
    editTitle: 'Edit IM Bot',
    selectKey: 'Select an API key to bind',
    modelPlaceholder: 'Leave empty to use the group default model'
  },
  pair: {
    title: 'Pair code for "{name}"',
    hint: 'DM this bot on your IM platform and send the code to pair',
    expiresAt: 'Valid until {time} (single use)',
    failed: 'Failed to generate pairing code'
  },
  chats: {
    title: 'Chats of "{name}"',
    user: 'User',
    platformUser: 'Platform user ID',
    status: 'Status',
    lastMessage: 'Last message',
    active: 'active',
    blocked: 'blocked',
    history: 'History',
    block: 'Block',
    unblock: 'Unblock',
    unpair: 'Unpair',
    unpairConfirm: 'Unpair this user? They must pair again to continue.',
    historyTitle: 'Chat history — {user}',
    empty: 'No chats yet'
  },
  platforms: {
    telegram: 'Telegram',
    feishu: 'Feishu',
    dingtalk: 'DingTalk',
    wecom: 'WeCom',
    qq: 'QQ',
    slack: 'Slack',
    wechat: 'WeChat',
    whatsapp: 'WhatsApp'
  }
}
export default imBotsEn
