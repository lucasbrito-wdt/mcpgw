import type { Messages, PluralKey, Translator, Vars } from "../lib/i18n";

/** `key_one` for exactly 1, `key_other` for anything else (CLDR puts 0 in "one" for pt-BR, which would read "0 tool"). */
export function countLabel<E extends Messages>(t: Translator<E>, key: PluralKey<NoInfer<E>>, n: number, vars?: Vars): string {
  return t((n === 1 ? `${key}_one` : `${key}_other`) as keyof E & string, { n, ...vars });
}
