import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import { MockDataBanner } from '../src/feedback/MockDataBanner.js';

afterEach(() => {
  cleanup();
  vi.unstubAllEnvs();
});

describe('MockDataBanner', () => {
  it('renders with banner role', () => {
    render(<MockDataBanner />);
    expect(screen.getByRole('banner')).toBeTruthy();
  });

  it('shows fixture-mode warning text', () => {
    render(<MockDataBanner />);
    expect(screen.getByRole('banner').textContent).toMatch(/Fixture mode/);
  });

  it('has accessible label describing fixture data', () => {
    render(<MockDataBanner />);
    expect(screen.getByRole('banner').getAttribute('aria-label')).toMatch(/fixture data/i);
  });

  it('applies extra className alongside base classes', () => {
    render(<MockDataBanner className="my-class" />);
    expect(screen.getByRole('banner').className).toContain('my-class');
    expect(screen.getByRole('banner').className).toContain('bg-amber-50');
  });

  it('throws in production builds', () => {
    vi.stubEnv('NODE_ENV', 'production');
    expect(() => render(<MockDataBanner />)).toThrow(
      'MockDataBanner must not render in production builds',
    );
  });
});
