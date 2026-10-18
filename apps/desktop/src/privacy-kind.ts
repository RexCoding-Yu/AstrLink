import { i18n } from "./i18n";

/**
 * The privacy kinds Core records, in the order the UI lists them. A hit or a
 * finding carries one of these; anything else is a newer Core label.
 */
export const PRIVACY_KIND_ORDER = [
  "email",
  "phone",
  "account",
  "payment_card",
  "ip_address",
  "url",
  "common_secret",
  "private_address",
  "private_date",
  "private_person",
  "unknown",
] as const;

export type PrivacyKind = (typeof PRIVACY_KIND_ORDER)[number];

const knownKinds = new Set<string>(PRIVACY_KIND_ORDER);

/** A kind's own name, so an unrecognized label still reads as itself. */
export function privacyKindLabel(kind: string): string {
  return knownKinds.has(kind) ? i18n.t(`privacy.${kind}`) : kind;
}

/** Sorts kinds the UI knows about first, then unknown labels alphabetically. */
export function sortPrivacyKinds(kinds: string[]): string[] {
  return [...kinds].sort((left, right) => {
    const leftIndex = PRIVACY_KIND_ORDER.indexOf(left as PrivacyKind);
    const rightIndex = PRIVACY_KIND_ORDER.indexOf(right as PrivacyKind);
    if (leftIndex !== -1 && rightIndex !== -1) return leftIndex - rightIndex;
    if (leftIndex !== -1) return -1;
    if (rightIndex !== -1) return 1;
    return left.localeCompare(right);
  });
}
