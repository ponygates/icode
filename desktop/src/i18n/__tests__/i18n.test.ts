import { describe, it, expect } from 'vitest';
import i18n from '../index';

type Bundle = Record<string, unknown>;

function flattenKeys(obj: unknown, prefix = ''): string[] {
  if (obj === null || typeof obj !== 'object') return [prefix];
  const keys: string[] = [];
  for (const [key, value] of Object.entries(obj as Bundle)) {
    const path = prefix ? `${prefix}.${key}` : key;
    keys.push(...flattenKeys(value, path));
  }
  return keys;
}

function bundleKeys(lang: string): string[] {
  const resources = (i18n.options.resources as Record<string, Record<string, Bundle>>) ?? {};
  const bundle = resources[lang]?.translation;
  if (!bundle) throw new Error(`No translation bundle found for locale "${lang}"`);
  return flattenKeys(bundle).sort();
}

describe('i18n locale parity', () => {
  const locales = ['zh-CN', 'en', 'zh-TW'];

  it.each(locales)('has a translation bundle for %s', (lang) => {
    expect(() => bundleKeys(lang)).not.toThrow();
  });

  it('all locales expose the same translation key set', () => {
    const sets = locales.map((lang) => new Set(bundleKeys(lang)));
    const [baseline, ...rest] = sets;
    const baselineKeys = [...baseline!].sort();

    const missing: string[] = [];
    const extra: string[] = [];

    for (const key of baselineKeys) {
      for (const other of rest) {
        if (!other.has(key)) missing.push(key);
      }
    }
    for (const other of rest) {
      for (const key of other) {
        if (!baseline.has(key)) extra.push(key);
      }
    }

    expect(
      { missingInOtherLocales: [...new Set(missing)], onlyInOtherLocales: [...new Set(extra)] }
    ).toEqual({ missingInOtherLocales: [], onlyInOtherLocales: [] });
  });

  it('i18n key set has no duplicate leaf paths (would shadow earlier keys)', () => {
    for (const lang of locales) {
      const keys = bundleKeys(lang);
      const dupes = keys.filter((k, i) => keys.indexOf(k) !== i);
      expect({ lang, duplicateKeys: [...new Set(dupes)] }).toEqual({ lang, duplicateKeys: [] });
    }
  });

  // The en bundle must be genuinely translated — a stray Chinese value renders
  // mid-English UI. Language names (简体中文/繁體中文) stay in native script.
  it('en bundle values contain no untranslated CJK text', () => {
    const resources = (i18n.options.resources as Record<string, Record<string, Bundle>>) ?? {};
    const en = resources['en']?.translation;
    const allowed = /(^|\.)(lang)\./;
    const cjk = /[\u4e00-\u9fff]/;
    const offenders: string[] = [];
    const walk = (obj: unknown, prefix = '') => {
      for (const [key, value] of Object.entries(obj as Bundle)) {
        const path = prefix ? `${prefix}.${key}` : key;
        if (typeof value === 'string') {
          if (cjk.test(value) && !allowed.test(path)) offenders.push(`${path} = ${value}`);
        } else if (value && typeof value === 'object') {
          walk(value, path);
        }
      }
    };
    walk(en);
    expect(offenders).toEqual([]);
  });
});
