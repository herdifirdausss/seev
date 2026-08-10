import { format } from '@seev/money';
import type { Money } from '@seev/money';

interface MoneyTextProps {
  value: Money;
  locale?: string;
  showSign?: boolean;
  className?: string;
}

// Displays exact minor-unit money — never uses binary floating point for display.
// aria-label provides the full value for screen readers.
export function MoneyText({ value, locale, showSign, className }: MoneyTextProps) {
  const formatted = format(value, {
    ...(locale !== undefined ? { locale } : {}),
    ...(showSign !== undefined ? { showSign } : {}),
  });
  return (
    <span
      className={className}
      aria-label={`${value.amountMinor.toString()} ${value.currency} minor units`}
      style={{ fontVariantNumeric: 'tabular-nums' }}
    >
      {formatted}
    </span>
  );
}

interface SignedMoneyProps extends MoneyTextProps {
  positiveClassName?: string;
  negativeClassName?: string;
}

export function SignedMoney({
  value,
  positiveClassName = 'text-green-700',
  negativeClassName = 'text-red-700',
  ...rest
}: SignedMoneyProps) {
  const colorClass = value.amountMinor >= 0n ? positiveClassName : negativeClassName;
  return (
    <MoneyText
      {...rest}
      value={value}
      showSign
      className={`${colorClass} ${rest.className ?? ''}`}
    />
  );
}
