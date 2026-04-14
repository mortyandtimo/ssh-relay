export interface Certificate {
  id: string;
  userId: string;
  domain: string;
  certPem: string;
  keyPem?: string;
  issuer: string;
  autoRenew: boolean;
  expiresAt?: string;
  dnsVerifiedAt?: string;
  lastRenewedAt?: string;
  renewError?: string;
  createdAt: string;
  updatedAt: string;
}

export interface DNSCheckResult {
  resolved: boolean;
  ips: string[];
  matches: boolean;
}

export interface User {
  id: string;
  email: string;
  displayName: string;
  role: string;
  createdAt: string;
  updatedAt: string;
}

export interface HttpOutput {
  status: number;
  ok: boolean;
  body: string;
  setCookies: string[];
}
