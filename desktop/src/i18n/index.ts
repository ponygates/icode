import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';

i18n.use(initReactI18next).init({
  resources: {
    'zh-CN': {
      translation: {
        app: { name: 'iCode', tagline: '多模型 AI 编程 Agent' },
        sidebar: { chat: '对话', models: '模型', settings: '设置', sessions: '会话' },
        chat: {
          placeholder: '输入您的编程问题...',
          send: '发送',
          newSession: '新建会话',
          clearChat: '清空对话',
        },
        models: {
          title: '模型管理',
          refresh: '刷新列表',
          search: '搜索模型...',
          provider: '提供商',
          plan: '方案',
          free: '免费',
        },
        settings: {
          title: '设置',
          language: '语言',
          theme: '主题',
          appearance: '外观',
          provider: '提供商配置',
          apiKey: 'API 密钥',
          about: '关于 iCode',
        },
        lang: { zhCN: '简体中文', zhTW: '繁體中文', en: 'English' },
        token: {
          usage: 'Token 用量',
          input: '输入',
          output: '输出',
          cacheHit: '缓存命中',
          cost: '预估费用',
          free: '免费',
        },
      },
    },
    'zh-TW': {
      translation: {
        app: { name: 'iCode', tagline: '多模型 AI 程式開發 Agent' },
        sidebar: { chat: '對話', models: '模型', settings: '設定', sessions: '對話' },
        chat: {
          placeholder: '輸入您的程式開發問題...',
          send: '傳送',
          newSession: '新增對話',
          clearChat: '清除對話',
        },
        models: {
          title: '模型管理',
          refresh: '重新整理列表',
          search: '搜尋模型...',
          provider: '提供者',
          plan: '方案',
          free: '免費',
        },
        settings: {
          title: '設定',
          language: '語言',
          theme: '主題',
          appearance: '外觀',
          provider: '提供者設定',
          apiKey: 'API 金鑰',
          about: '關於 iCode',
        },
        lang: { zhCN: '简体中文', zhTW: '繁體中文', en: 'English' },
        token: {
          usage: 'Token 用量',
          input: '輸入',
          output: '輸出',
          cacheHit: '快取命中',
          cost: '預估費用',
          free: '免費',
        },
      },
    },
    en: {
      translation: {
        app: { name: 'iCode', tagline: 'Multi-Model AI Coding Agent' },
        sidebar: { chat: 'Chat', models: 'Models', settings: 'Settings', sessions: 'Sessions' },
        chat: {
          placeholder: 'Enter your coding question...',
          send: 'Send',
          newSession: 'New Session',
          clearChat: 'Clear Chat',
        },
        models: {
          title: 'Model Management',
          refresh: 'Refresh List',
          search: 'Search models...',
          provider: 'Provider',
          plan: 'Plan',
          free: 'Free',
        },
        settings: {
          title: 'Settings',
          language: 'Language',
          theme: 'Theme',
          appearance: 'Appearance',
          provider: 'Provider Config',
          apiKey: 'API Key',
          about: 'About iCode',
        },
        lang: { zhCN: '简体中文', zhTW: '繁體中文', en: 'English' },
        token: {
          usage: 'Token Usage',
          input: 'Input',
          output: 'Output',
          cacheHit: 'Cache Hit',
          cost: 'Est. Cost',
          free: 'Free',
        },
      },
    },
  },
  lng: 'zh-CN',
  fallbackLng: 'en',
  interpolation: { escapeValue: false },
});

export default i18n;
