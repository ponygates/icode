// highlight.js ships types only for its main entry; the per-language and
// core subpath imports used by Markdown.tsx have no declarations. Their
// runtime shape is `LanguageFn`, which matches hljs.registerLanguage.
declare module 'highlight.js/lib/core';
declare module 'highlight.js/lib/languages/*';
