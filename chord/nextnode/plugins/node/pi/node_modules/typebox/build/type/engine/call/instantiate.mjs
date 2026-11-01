// deno-fmt-ignore-file
import { Settings } from '../../../system/settings/index.mjs';
import { Guard } from '../../../guard/index.mjs';
import { CallConstruct } from '../../types/call.mjs';
import { Ref } from '../../types/ref.mjs';
import { IsGeneric } from '../../types/generic.mjs';
import { EvaluateUnion } from '../evaluate/index.mjs';
import { InstantiateType } from '../instantiate.mjs';
import { InstantiateTypes } from '../instantiate.mjs';
import { State } from '../instantiate.mjs';
// ------------------------------------------------------------------
// Infrastructure
// ------------------------------------------------------------------
import { DistributeArguments } from './distribute_arguments.mjs';
import { ResolveTarget } from './resolve_target.mjs';
import { ResolveArgumentsContext } from './resolve_arguments.mjs';
// ------------------------------------------------------------------
// InstantiationGuard
// ------------------------------------------------------------------
let instantiationDepth = 0;
let instantiationCount = 0;
function InstantiationAssert() {
    if (Guard.IsLessThan(instantiationCount, Settings.Get().maxInstantiationCount))
        return;
    throw Error('Type instantiation is excessively deep and possibly infinite');
}
function InstantiationIncrement() {
    InstantiationAssert();
    instantiationCount++;
    instantiationDepth++;
}
function InstantiationDecrement() {
    instantiationDepth--;
    if (Guard.IsEqual(instantiationDepth, 0))
        instantiationCount = 0;
}
function Peek(state) {
    const result = Guard.IsGreaterThan(state.callstack.length, 0) ? state.callstack[state.callstack.length - 1] : '';
    return result;
}
function IsTailCall(state, name) {
    const result = Guard.IsEqual(Peek(state), name);
    return result;
}
function CallDispatch(context, state, target, parameters, expression, arguments_) {
    InstantiationIncrement();
    try {
        const argumentsContext = ResolveArgumentsContext(context, state, parameters, arguments_);
        const returnType = InstantiateType(argumentsContext, State([...state['callstack'], target['$ref']], state['visited']), expression);
        return InstantiateType(argumentsContext, State([], []), returnType);
    }
    finally {
        InstantiationDecrement();
    }
}
function CallDistributed(context, state, target, parameters, expression, distributedArguments) {
    return distributedArguments.reduce((result, arguments_) => {
        const returnType = CallDispatch(context, state, target, parameters, expression, arguments_);
        return [...result, returnType];
    }, []);
}
function CallImmediate(context, state, target, parameters, expression, arguments_) {
    const distributedArguments = DistributeArguments(parameters, arguments_, expression);
    const returnTypes = CallDistributed(context, state, target, parameters, expression, distributedArguments);
    const result = Guard.IsEqual(returnTypes.length, 1) ? returnTypes[0] : EvaluateUnion(returnTypes);
    return result;
}
export function CallInstantiate(context, state, target, arguments_) {
    const instantiatedArguments = InstantiateTypes(context, state, arguments_);
    const resolved = ResolveTarget(context, target, arguments_);
    const name = resolved[0];
    const type = resolved[1];
    const result = (IsGeneric(type)
        ? IsTailCall(state, name)
            ? CallConstruct(Ref(name), instantiatedArguments)
            : CallImmediate(context, state, Ref(name), type.parameters, type.expression, instantiatedArguments)
        : CallConstruct(target, instantiatedArguments));
    return result;
}
