export const imBotsZh = {
  title: 'IM 机器人',
  description: '把 Telegram / 飞书等 IM 私聊接入网关，手机上直接与绑定模型的机器人聊天',
  create: '创建机器人',
  loadFailed: '加载机器人列表失败',
  saved: '已保存',
  saveFailed: '保存失败',
  deleteConfirm: '确定删除机器人「{name}」？其聊天历史将一并删除。',
  deleteFailed: '删除失败',
  toggleFailed: '启停失败',
  testOk: '连接测试通过',
  testFailed: '连接测试失败',
  empty: '还没有机器人，点击右上角创建',
  comingSoon: '暂未开放',
  groupDefault: '分组默认',
  status: {
    enabled: '运行中',
    disabled: '已停用',
    error: '异常'
  },
  actions: {
    test: '测试',
    enable: '启用',
    disable: '停用',
    pairCode: '配对码',
    chats: '会话'
  },
  statusLabel: '状态',
  fields: {
    name: '名称',
    platform: '平台',
    apiKey: '绑定 API Key',
    model: '模型覆盖',
    systemPrompt: '系统提示词',
    maxConcurrency: '并发上限',
    historyMax: '上下文条数',
    pairingEnabled: '需要配对码（默认拒绝陌生人）'
  },
  form: {
    createTitle: '创建 IM 机器人',
    editTitle: '编辑 IM 机器人',
    selectKey: '选择要绑定的 API Key',
    modelPlaceholder: '留空使用分组默认模型'
  },
  pair: {
    title: '「{name}」配对码',
    hint: '在 IM 里私聊该机器人，发送此配对码完成绑定',
    expiresAt: '有效期至 {time}（一次性）',
    failed: '生成配对码失败'
  },
  chats: {
    title: '「{name}」的会话',
    user: '用户',
    platformUser: '平台用户 ID',
    status: '状态',
    lastMessage: '最近消息',
    active: '正常',
    blocked: '已封禁',
    history: '历史',
    block: '封禁',
    unblock: '解封',
    unpair: '解绑',
    unpairConfirm: '确定解除该用户配对？对方需重新配对才能继续使用。',
    historyTitle: '{user} 的聊天记录',
    empty: '暂无会话'
  },
  platforms: {
    telegram: 'Telegram',
    feishu: '飞书',
    dingtalk: '钉钉',
    wecom: '企业微信',
    qq: 'QQ',
    slack: 'Slack',
    wechat: '微信',
    whatsapp: 'WhatsApp'
  }
}
export default imBotsZh
