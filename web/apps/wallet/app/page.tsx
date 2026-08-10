import type { Metadata } from 'next';

export const metadata: Metadata = {
  title: 'Seev Wallet — Reference Implementation',
};

export default function HomePage() {
  return (
    <div className="max-w-2xl mx-auto p-8">
      <h1 className="text-2xl font-semibold mb-4">Seev Wallet</h1>
      <p className="text-gray-600 mb-6">
        This is a reference implementation of a wallet application. It demonstrates the frontend
        platform foundation and does not represent a live product.
      </p>
      <div className="bg-amber-50 border border-amber-200 rounded p-4 text-sm text-amber-900 mb-6">
        <strong>Not a real wallet.</strong> Seev is an open-source learning project under
        Apache-2.0. It is not a licensed financial product, not certified for real money, and not
        suitable for production use.
      </div>
      <nav aria-label="Reference screens">
        <h2 className="text-lg font-medium mb-2">Reference screens (fixture data)</h2>
        <ul className="list-disc list-inside space-y-1">
          <li>
            <a href="/reference/transaction" className="text-blue-600 hover:underline">
              Transaction evidence reference
            </a>
          </li>
        </ul>
      </nav>
    </div>
  );
}
