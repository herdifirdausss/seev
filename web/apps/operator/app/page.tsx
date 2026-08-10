import type { Metadata } from 'next';

export const metadata: Metadata = {
  title: 'Seev Operator — Reference Implementation',
};

export default function HomePage() {
  return (
    <div className="max-w-3xl mx-auto p-8">
      <h1 className="text-xl font-semibold mb-4">Seev Operator Console</h1>
      <div className="bg-blue-50 border border-blue-200 rounded p-4 text-sm text-blue-900 mb-6">
        <strong>Foundation scaffold only.</strong> The current authoritative operator console is the
        Admin BFF embedded HTML/htmx console. This application is a platform foundation that does
        not yet implement any live operator journey.
      </div>
      <p className="text-gray-600 mb-4">
        The current operator console is accessible at the Admin BFF address. This application shell
        will grow to support modern operator views once feature parity and security review are
        complete.
      </p>
      <nav aria-label="Reference screens">
        <h2 className="text-base font-medium mb-2">Reference screens (fixture data)</h2>
        <ul className="list-disc list-inside space-y-1 text-sm">
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
