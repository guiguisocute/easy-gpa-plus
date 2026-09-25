export class ApiError extends Error {
  status: number
  code: string
  detail: unknown

  constructor(status: number, code: string, message: string, detail?: unknown) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.detail = detail
  }
}

export function fail(status: number, code: string, message: string, detail?: unknown): never {
  throw new ApiError(status, code, message, detail)
}
