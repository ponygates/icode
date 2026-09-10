import type { Model } from '../stores/appStore';

/**
 * Per-model settings, as edited in the ⚙️ model dialog.
 *
 * These are genuinely per-model: the engine layers them on top of the global
 * defaults (see conversation.Engine.applyModelParams), so editing one model
 * never disturbs the others.
 */
export interface ModelSettingsValues {
  apiKey: string;
  apiBase: string;
  temperature: number;
  /** Max output tokens for this model. */
  maxTokens: number;
  topP: number;
  /** 0 = leave the model's context window as-is. */
  contextWindow: number;
}

/**
 * Body for `PUT /api/config/model`.
 *
 * Extracted as a pure function because this payload is the contract with the
 * backend: `custom` has to be preserved (dropping it would silently demote a
 * hand-added model to a built-in override and remove it from the list), and
 * `model_id` must be the bare vendor id the provider accepts.
 */
export function buildModelPayload(
  model: Model,
  values: ModelSettingsValues,
): Record<string, unknown> {
  return {
    id: model.id,
    provider: model.provider,
    model_id: model.model_id || model.id,
    name: model.name,
    context_window: values.contextWindow,
    max_output_tokens: values.maxTokens,
    temperature: values.temperature,
    top_p: values.topP,
    custom: !!model.custom,
  };
}

async function errorMessage(res: Response, fallback: string): Promise<string> {
  const data = await res.json().catch(() => null);
  const detail = data && typeof data.error === 'string' ? data.error : '';
  return detail || `${fallback} ${res.status}`;
}

/**
 * Persist per-model settings.
 *
 * Two endpoints, two different owners:
 *   - `/api/config/model` → the per-model parameters (the engine reads these)
 *   - `/api/config/key`   → provider credentials
 *
 * The parameters used to be sent to `/api/config/key`, which only decodes
 * provider/api_key/api_base — so temperature/top_p/max_output were dropped on
 * the floor while the dialog still reported 已保存. That is the bug this split
 * fixes.
 *
 * Credentials are only written when the user actually typed something, so an
 * untouched field never blanks a stored key.
 *
 * Returns '' on success, otherwise a message to surface in the dialog.
 */
export async function saveModelSettings(
  base: string,
  model: Model,
  values: ModelSettingsValues,
): Promise<string> {
  if (!base) return 'Backend not connected';
  try {
    const modelRes = await fetch(`${base}/api/config/model`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(buildModelPayload(model, values)),
    });
    if (!modelRes.ok) return errorMessage(modelRes, 'HTTP');

    if (values.apiKey || values.apiBase) {
      const keyRes = await fetch(`${base}/api/config/key`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          provider: model.provider,
          api_key: values.apiKey,
          api_base: values.apiBase,
        }),
      });
      if (!keyRes.ok) return errorMessage(keyRes, 'HTTP');
    }
    return '';
  } catch (e) {
    return e instanceof Error ? e.message : String(e);
  }
}
