// Dispatches the judgment-call review tools to the configured backend: the inbox/.review/ file
// queue (vault.ts, default) or the vault's GitHub issues (github.ts). The tool surface in mcp.ts
// is identical either way — only the storage behind it changes — so I answer the same from chat
// whether the host runs the file channel or the GitHub-issue channel.
import { REVIEW_CHANNEL } from './config.js';
import * as files from './vault.js';
import * as github from './github.js';

const impl = REVIEW_CHANNEL === 'github' ? github : files;

export const listQuestions = impl.listQuestions;
export const getQuestion = impl.getQuestion;
export const answerQuestion = impl.answerQuestion;
