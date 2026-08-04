package fs

/*
#include <windows.h>
#include <stdlib.h>

typedef long (*fsp_posix_set_uid_map_fn)(unsigned int Uid[], void *Sid[], unsigned long Count);

static int mapUid0ToCurrentUser(void) {
	HMODULE m = LoadLibraryW(L"winfsp-x64.dll");
	if (!m)
		return -1;
	fsp_posix_set_uid_map_fn fn = (fsp_posix_set_uid_map_fn)GetProcAddress(m, "FspPosixSetUidMap");
	if (!fn)
		return -2;
	HANDLE tok = NULL;
	if (!OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &tok))
		return -3;
	DWORD len = 0;
	GetTokenInformation(tok, TokenUser, NULL, 0, &len);
	if (len == 0) {
		CloseHandle(tok);
		return -4;
	}
	TOKEN_USER *tu = (TOKEN_USER *)malloc(len);
	if (!tu) {
		CloseHandle(tok);
		return -5;
	}
	if (!GetTokenInformation(tok, TokenUser, tu, len, &len)) {
		free(tu);
		CloseHandle(tok);
		return -6;
	}
	unsigned int uid[1] = { 0 };
	void *sid[1] = { (void *)tu->User.Sid };
	long r = fn(uid, sid, 1);
	free(tu);
	CloseHandle(tok);
	return (int)r;
}
*/
import "C"

func mapUid0ToCurrentUser() int {
	return int(C.mapUid0ToCurrentUser())
}
