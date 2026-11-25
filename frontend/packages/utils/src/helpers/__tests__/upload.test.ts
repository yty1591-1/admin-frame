import { afterEach, describe, expect, it, vi } from 'vitest';

import { downloadFile, getFileExtension, validateUploadFile } from '../upload';

function file(name: string, type: string) {
  return { name, type };
}

describe('getFileExtension', () => {
  it('returns lowercase extension with dot', () => {
    expect(getFileExtension('Report.PDF')).toBe('.pdf');
    expect(getFileExtension('a.tar.gz')).toBe('.gz');
  });

  it('returns empty string when there is no extension', () => {
    expect(getFileExtension('README')).toBe('');
    expect(getFileExtension(undefined)).toBe('');
  });
});

describe('validateUploadFile', () => {
  it('accepts an allowlisted image', () => {
    expect(validateUploadFile(file('avatar.png', 'image/png'))).toEqual({
      extension: '.png',
      mime: 'image/png',
      ok: true,
    });
  });

  it('rejects SVG by default', () => {
    expect(validateUploadFile(file('logo.svg', 'image/svg+xml'))).toEqual({
      extension: '.svg',
      mime: 'image/svg+xml',
      ok: false,
      reason: 'svg',
    });
  });

  it('allows SVG when explicitly opted in', () => {
    expect(
      validateUploadFile(file('logo.svg', 'image/svg+xml'), { allowSvg: true }),
    ).toMatchObject({ ok: true });
  });

  it('rejects an extension outside the allowlist', () => {
    expect(
      validateUploadFile(file('setup.exe', 'application/x-msdownload')),
    ).toMatchObject({ ok: false, reason: 'extension' });
  });

  it('rejects a misleading MIME even when the extension is allowed', () => {
    expect(validateUploadFile(file('photo.png', 'text/html'))).toMatchObject({
      ok: false,
      reason: 'mime',
    });
  });

  it('accepts when the browser reports no MIME', () => {
    expect(validateUploadFile(file('notes.txt', ''))).toMatchObject({
      ok: true,
    });
  });

  it('supports custom allowlists', () => {
    expect(
      validateUploadFile(file('data.json', 'application/json'), {
        extensions: ['.json'],
        mimeTypes: ['application/json'],
      }),
    ).toMatchObject({ ok: true });
  });
});

describe('downloadFile', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('creates a download anchor with noopener and clicks it', () => {
    const click = vi
      .spyOn(HTMLAnchorElement.prototype, 'click')
      .mockImplementation(() => {});
    const append = vi.spyOn(document.body, 'appendChild');

    downloadFile('https://example.com/files/report.pdf', 'report.pdf');

    const anchor = append.mock.calls[0]?.[0] as HTMLAnchorElement;
    expect(anchor).toBeInstanceOf(HTMLAnchorElement);
    expect(anchor.href).toBe('https://example.com/files/report.pdf');
    expect(anchor.download).toBe('report.pdf');
    expect(anchor.rel).toBe('noopener noreferrer');
    expect(anchor.target).toBe('_blank');
    expect(click).toHaveBeenCalledTimes(1);
  });

  it('does nothing for empty urls', () => {
    const append = vi.spyOn(document.body, 'appendChild');
    downloadFile('');
    expect(append).not.toHaveBeenCalled();
  });
});
