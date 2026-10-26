import { zhCN, type MessageKey } from './messages/zh-CN';

export type TranslateParams = Readonly<Record<string, string | number>>;

export type Translator = ((key: MessageKey, params?: TranslateParams) => string) & {
  /** 开发/单测诊断：本次进程内未命中的 key（去重，不输出控制台）。 */
  readonly missingKeys: readonly string[];
};

const PLACEHOLDER = /\{(\w+)\}/g;

/**
 * 创建翻译器（前端架构 §11.4）：
 * - 缺失 key 返回 key 本身，避免页面出现空白；
 * - 仅支持 `{name}` 插值，不做函数式/富文本插值；
 * - 缺失参数保留原占位符，便于发现遗漏。
 */
export function createTranslator(messages: Readonly<Record<string, string>> = zhCN): Translator {
  const missingKeys: string[] = [];

  const translate = (key: MessageKey, params?: TranslateParams): string => {
    const template = messages[key];
    if (template === undefined) {
      if (!missingKeys.includes(key)) {
        missingKeys.push(key);
      }
      return key;
    }
    if (params === undefined) {
      return template;
    }
    return template.replace(PLACEHOLDER, (placeholder: string, name: string) => {
      const value = params[name];
      return value === undefined ? placeholder : String(value);
    });
  };

  return Object.assign(translate, { missingKeys });
}

/** 无 Provider 时的默认翻译器（packages/ui 与纯函数场景复用）。 */
export const defaultTranslator: Translator = createTranslator();
