// Visible fixture-mode indicator — K16: every mock environment shows this.
// Must be impossible to miss and must not appear in production builds.

interface MockDataBannerProps {
  className?: string;
}

export function MockDataBanner({ className = '' }: MockDataBannerProps) {
  if (process.env.NODE_ENV === 'production') {
    throw new Error('MockDataBanner must not render in production builds');
  }

  return (
    <div
      role="banner"
      aria-label="This page displays fixture data, not live information"
      className={`w-full bg-amber-50 border-b border-amber-300 px-4 py-2 text-sm text-amber-900 font-medium ${className}`}
    >
      ⚠ Fixture mode — data is synthetic and does not reflect live balances or transactions
    </div>
  );
}
