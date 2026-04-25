export class FookieClient {
  endpoint;
  adminKey;
  headers;
  constructor(options = {}) {
    this.endpoint = options.endpoint ?? 'http://localhost:8080/graphql';
    this.adminKey = options.adminKey;
    this.headers = options.headers ?? {};
  }
  async request(payload) {
    const headers = {
      'content-type': 'application/json',
      ...this.headers,
    };
    if (this.adminKey) {
      headers['admin_key'] = this.adminKey;
      headers['X-Fookie-Admin-Key'] = this.adminKey;
    }
    const res = await fetch(this.endpoint, {
      method: 'POST',
      headers,
      body: JSON.stringify(payload),
    });
    return await res.json();
  }
}
