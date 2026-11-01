// deno-fmt-ignore-file
import { Guard } from '../../../guard/index.mjs';
import { IsDependent } from '../../types/dependent.mjs';
import { IsEnum } from '../../types/enum.mjs';
import { IsIntersect } from '../../types/intersect.mjs';
import { Literal } from '../../types/literal.mjs';
import { Never } from '../../types/never.mjs';
import { IsTemplateLiteral } from '../../types/template_literal.mjs';
import { Union, IsUnion } from '../../types/union.mjs';
// ------------------------------------------------------------------
// Infrastructure
// ------------------------------------------------------------------
import { Distribute } from './distribute.mjs';
import { Broaden } from './broaden.mjs';
import { ExcludeOperation } from '../exclude/operation.mjs';
import { TemplateLiteralDecode } from '../template_literal/decode.mjs';
export function EvaluateDependent(if_, then_, else_) {
    const intersected = EvaluateIntersect([if_, then_]);
    const excluded = ExcludeOperation(else_, if_);
    const result = EvaluateUnion([intersected, excluded]);
    return result;
}
export function EvaluateEnum(values, result = []) {
    return Guard.ShiftLeft(values, (left, right) => EvaluateEnum(right, [...result, Literal(left)]), () => EvaluateUnion(result));
}
export function EvaluateIntersect(types) {
    const distribution = Distribute(types);
    const broadend = Broaden(distribution);
    const result = EvaluateUnion(broadend);
    return result;
}
export function EvaluateTemplateLiteral(pattern) {
    const evaluated = TemplateLiteralDecode(pattern);
    const result = EvaluateType(evaluated);
    return result;
}
export function EvaluateUnion(types) {
    const broadend = Broaden(types);
    const result = EvaluateUnionFast(broadend);
    return result;
}
export function EvaluateType(type) {
    const result = (IsDependent(type) ? EvaluateDependent(type.if, type.then, type.else) :
        IsEnum(type) ? EvaluateEnum(type.enum) :
            IsIntersect(type) ? EvaluateIntersect(type.allOf) :
                IsTemplateLiteral(type) ? EvaluateTemplateLiteral(type.pattern) :
                    IsUnion(type) ? EvaluateUnion(type.anyOf) :
                        type);
    return result;
}
export function EvaluateUnionFast(types) {
    const result = (Guard.IsEqual(types.length, 1) ? types[0] :
        Guard.IsEqual(types.length, 0) ? Never() :
            Union(types));
    return result;
}
