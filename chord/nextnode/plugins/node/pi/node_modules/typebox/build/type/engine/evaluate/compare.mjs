// deno-lint-ignore-file ban-types
// deno-fmt-ignore-file
import { Extends, ExtendsResult } from "../../extends/index.mjs";
// ------------------------------------------------------------------
// TCompare
// ------------------------------------------------------------------
export const CompareResultEqual = 0; // 'equal'
export const CompareResultDisjoint = 1; // 'disjoint'
export const CompareResultLeftInside = 2; // 'left-inside'
export const CompareResultRightInside = 3; // 'right-inside'
/** Compares left and right types and determines their set relationship. */
export function Compare(left, right) {
    const extendsCheck = [Extends({}, left, right), Extends({}, right, left)];
    return (ExtendsResult.IsExtendsTrueLike(extendsCheck[0]) && ExtendsResult.IsExtendsTrueLike(extendsCheck[1]) ? CompareResultEqual :
        ExtendsResult.IsExtendsTrueLike(extendsCheck[0]) && ExtendsResult.IsExtendsFalse(extendsCheck[1]) ? CompareResultLeftInside :
            ExtendsResult.IsExtendsFalse(extendsCheck[0]) && ExtendsResult.IsExtendsTrueLike(extendsCheck[1]) ? CompareResultRightInside :
                CompareResultDisjoint);
}
