import type { Metadata } from 'next';
import type React from 'react';

export const metadata: Metadata = {
  title: 'Seev Operator',
  description: 'Operator console — reference implementation, not for production use.',
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
        <header role="banner" className="bg-gray-900 text-white px-6 py-3 flex items-center gap-4">
          <span className="font-semibold text-sm tracking-wide uppercase">Seev Operator</span>
          <span className="text-gray-400 text-xs">|</span>
          <span className="text-yellow-400 text-xs font-medium">Reference implementation</span>
        </header>
        <main id="main-content" className="min-h-screen">
          {children}
        </main>
      </body>
    </html>
  );
}
