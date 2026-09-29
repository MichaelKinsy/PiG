import { getNativePlatformHelper } from "./native-platform.js";
export function isNativeModifierPressed(key) {
    const helper = getNativePlatformHelper();
    if (!helper?.isModifierPressed)
        return false;
    try {
        return helper.isModifierPressed(key) === true;
    }
    catch {
        return false;
    }
}
//# sourceMappingURL=native-modifiers.js.map