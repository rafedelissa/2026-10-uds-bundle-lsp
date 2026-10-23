import { commands, workspace, ExtensionContext } from 'vscode';
import { LanguageClient, LanguageClientOptions, ServerOptions } from 'vscode-languageclient/node';

let client: LanguageClient | undefined;

const documentPatterns = [
    '**/{bundle,defaults,config}.uds.hcl',
    '**/*.{yaml,yml}'
];

export async function activate(context: ExtensionContext): Promise<void> {
    const configuration = workspace.getConfiguration('udsLsp');
    const serverOptions: ServerOptions = {
        command: configuration.get<string>('serverPath', 'uds-lsp'),
        args: configuration.get<string[]>('serverArgs', [])
    };

    const fileWatchers = documentPatterns.map(pattern => workspace.createFileSystemWatcher(pattern));
    context.subscriptions.push(...fileWatchers);

    const clientOptions: LanguageClientOptions = {
        documentSelector: documentPatterns.map(pattern => ({ scheme: 'file', pattern })),
        synchronize: {
            fileEvents: fileWatchers
        }
    };

    client = new LanguageClient(
        'udsLsp',
        'UDS LSP',
        serverOptions,
        clientOptions
    );

    context.subscriptions.push(commands.registerCommand(
        'uds-lsp-extension.restartServer',
        () => client?.restart()
    ));

    await client.start();
}

export function deactivate(): Thenable<void> | undefined {
    if (!client) {
        return undefined;
    }
    return client.stop();
}
