'use client';

import { useEffect } from 'react';

export default function ErrorPage({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  useEffect(() => {
    // Log without exposing stack trace or raw error detail to the user.
    console.error('[Wallet] Render error occurred, digest:', error.digest);
  }, [error]);

  return (
    <div className="max-w-2xl mx-auto p-8">
      <h2 className="text-xl font-semibold mb-2">Something went wrong</h2>
      <p className="text-gray-600 mb-4">
        An unexpected error occurred. Please try again or contact support if the problem persists.
      </p>
      {error.digest && (
        <p className="text-xs text-gray-400 mb-4 font-mono">Error reference: {error.digest}</p>
      )}
      <button
        onClick={reset}
        className="px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700 focus:outline-none focus:ring-2 focus:ring-blue-500"
      >
        Try again
      </button>
    </div>
  );
}
