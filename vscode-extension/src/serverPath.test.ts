import assert from 'node:assert/strict';
import { resolve } from 'node:path';
import { test } from 'node:test';
import { resolveServerPath } from './serverPath';

const extensionPath = resolve('checkout with spaces', 'vscode-extension');

test('installed extension resolves the bundled server independently of the workspace', () => {
    for (const configuredPath of [undefined, '']) {
        assert.equal(resolveServerPath({ configuredPath, extensionPath, development: false }),
            resolve(extensionPath, 'bin', process.platform === 'win32' ? 'uds-lsp.exe' : 'uds-lsp'));
    }
});

test('development sessions use the repository build', () => {
    assert.equal(resolveServerPath({ extensionPath, development: true, platform: 'linux' }),
        resolve(extensionPath, '..', 'build', 'uds-lsp'));
});

test('Windows uses an executable with the exe suffix in both modes', () => {
    for (const development of [true, false]) {
        assert.equal(resolveServerPath({ extensionPath, development, platform: 'win32' }),
            resolve(extensionPath, development ? '../build' : 'bin', 'uds-lsp.exe'));
    }
});

test('explicit executable paths and PATH commands override both defaults', () => {
    for (const development of [true, false]) {
        for (const configuredPath of ['uds-lsp', resolve('custom build', 'uds-lsp')]) {
            assert.equal(resolveServerPath({ configuredPath, extensionPath, development }), configuredPath);
        }
    }
});
