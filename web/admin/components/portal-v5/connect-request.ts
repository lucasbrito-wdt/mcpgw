import { ApiError } from "../../lib/api";
import { getLocale, translate } from "../../lib/i18n";
import { portalConnect } from "../../lib/i18n/messages/portalConnect";

const message = (key: keyof typeof portalConnect.en): string => translate(getLocale(), portalConnect, key);

/** What a failed connect call means for the dialog: the password was refused, or a message to show. */
export function describeConnectError(error: unknown): { passwordRefused: boolean; message: string } {
  if (error instanceof ApiError) {
    if (error.code === "reauth_required") return { passwordRefused: true, message: message("passwordWrong") };
    if (error.status === 429) return { passwordRefused: false, message: message("tooManyAttempts") };
    return { passwordRefused: false, message: error.fields?.token ?? error.message };
  }
  return { passwordRefused: false, message: message("unreachable") };
}
