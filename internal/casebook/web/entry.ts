// entry.ts — the esbuild entry point.
// Loads the app and boots it.

import { boot, registerSection, registerDock } from './app.ts';
import { makeApply } from './apply.ts';
import { makeAttention } from './attention.ts';
import { makeDock } from './dock.ts';
import { makeRules } from './rules.ts';

registerSection(makeAttention);
registerSection(makeRules);
registerSection(makeApply);
registerDock(makeDock);
boot();
