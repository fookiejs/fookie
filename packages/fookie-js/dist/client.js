import { subscribe as wsSubscribe } from './subscribe.js';
export class FookieClient {
    url;
    wsUrl;
    headers;
    connectionParams;
    constructor(opts) {
        this.url = opts.url;
        if (opts.wsUrl) {
            this.wsUrl = opts.wsUrl;
        }
        else {
            const httpUrl = opts.url.replace(/^http/, 'ws');
            this.wsUrl = httpUrl.endsWith('/ws') ? httpUrl : httpUrl + '/ws';
        }
        this.headers = { 'Content-Type': 'application/json' };
        this.connectionParams = {};
        if (opts.adminKey) {
            this.headers['X-Fookie-Admin-Key'] = opts.adminKey;
            this.connectionParams['adminKey'] = opts.adminKey;
        }
        if (opts.token) {
            this.headers['Authorization'] = `Bearer ${opts.token}`;
            this.connectionParams['token'] = opts.token;
        }
    }
    async query(gql, variables) {
        return this._send(gql, variables);
    }
    async mutate(gql, variables) {
        return this._send(gql, variables);
    }
    subscribe(gql, onData, variables) {
        return wsSubscribe({
            wsUrl: this.wsUrl,
            connectionParams: this.connectionParams,
            query: gql,
            variables,
            onData,
        });
    }
    async _send(query, variables) {
        const res = await fetch(this.url, {
            method: 'POST',
            headers: this.headers,
            body: JSON.stringify({ query, variables }),
        });
        if (!res.ok) {
            throw new Error(`HTTP ${res.status}: ${res.statusText}`);
        }
        const json = await res.json();
        if (json.errors && json.errors.length > 0) {
            throw new Error(json.errors.map((e) => e.message).join('; '));
        }
        return json.data;
    }
}
//# sourceMappingURL=client.js.map