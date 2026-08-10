import type { Metadata } from 'next';
import type React from 'react';

export const metadata: Metadata = {
  title: 'Seev Wallet',
  description:
    'Reference implementation — not a licensed financial product. ' +
    'This is an open-source learning project under Apache-2.0.',
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>
        <a
          href="#main-content"
          className="sr-only focus:not-sr-only focus:fixed focus:top-4 focus:left-4 focus:z-50 focus:p-2 focus:bg-white focus:text-black"
        >
          Skip to main content
        </a>
        <main id="main-content">{children}</main>
      </body>
    </html>
  );
}
