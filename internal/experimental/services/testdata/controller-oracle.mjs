import { createAgentController } from '../../../../.upstream/current/packages/coding-agent/src/experimental/services/agent-controller-provider.ts';

const responses = [];
for (const status of ['completed', 'declined', 'aborted', 'failed', 'suspended']) {
  const controller = createAgentController({prompt: async () => ({ok: true, value: {
    operationId: 'op', status, error: {code: 'provider', message: 'failed', details: {private: true}},
  }})});
  responses.push(await controller.prompt({message: 'hello', images: null}, {}));
}
for (const _tag of ['LaneBusy', 'InvalidMessage', 'UnknownSkill', 'UnknownTemplate', 'NothingToCompact', 'NothingToResume', 'InvalidNavigation', 'UnknownTarget', 'Closed', 'NoActiveOperation']) {
  const controller = createAgentController({prompt: async () => ({ok: false, error: {
    _tag, message: 'failure', ...(_tag === 'LaneBusy' ? {operationId: 'busy'} : {}),
  }})});
  responses.push(await controller.prompt({message: 'hello', images: null}, {}));
}
console.log(JSON.stringify(responses));
