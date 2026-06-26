//go:build windows


// Package win32 implements Windows-specific security primitives for sandboxing:
// - Restricted tokens with privilege stripping and low integrity level
// - Process mitigation policies (DEP, ACG, CIG, etc.)
// - Job Object UI restrictions (clipboard, desktop, hooks)
// - Environment variable whitelisting
package win32

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
	"k8s.io/klog/v2"
)

var (
	kernel32                       = windows.NewLazyDLL("kernel32.dll")
	advapi32                       = windows.NewLazyDLL("advapi32.dll")
	procSetProcessMitigationPolicy = kernel32.NewProc("SetProcessMitigationPolicy")
	procSetTokenInformation        = advapi32.NewProc("SetTokenInformation")
	procCreateProcessWithTokenW    = advapi32.NewProc("CreateProcessWithTokenW")
)

// Token related constants
const (
	TokenIntegrityLevel = 25

	SECURITY_MANDATORY_LABEL_AUTHORITY_RID = 16
	SECURITY_MANDATORY_LOW_RID             = 0x00001000

	SE_PRIVILEGE_REMOVED = 0x00000004

	// Job Object UI restrictions
	JOB_OBJECT_UILIMIT_HANDLES          = 0x00000001
	JOB_OBJECT_UILIMIT_READCLIPBOARD    = 0x00000002
	JOB_OBJECT_UILIMIT_WRITECLIPBOARD   = 0x00000004
	JOB_OBJECT_UILIMIT_SYSTEMPARAMETERS = 0x00000008
	JOB_OBJECT_UILIMIT_DISPLAYSETTINGS  = 0x00000010
	JOB_OBJECT_UILIMIT_GLOBALATOMS      = 0x00000020
	JOB_OBJECT_UILIMIT_DESKTOP          = 0x00000040
	JOB_OBJECT_UILIMIT_EXITWINDOWS      = 0x00000080

	// Process mitigation policies
	ProcessDEPPolicy                   = 0
	ProcessASLRPolicy                  = 1
	ProcessDynamicCodePolicy           = 2
	ProcessStrictHandleCheckPolicy     = 3
	ProcessExtensionPointDisablePolicy = 6
	ProcessSignaturePolicy             = 8
	ProcessFontDisablePolicy           = 9
	ProcessImageLoadPolicy             = 10
	ProcessChildProcessPolicy          = 13

	// DEP policy flags
	PROCESS_DEP_ENABLE                      = 0x00000001
	PROCESS_DEP_DISABLE_ATL_THUNK_EMULATION = 0x00000002

	// ASLR policy flags
	PROCESS_ASLR_ENABLE_RELOCATE_IMAGES = 0x00000001
	PROCESS_ASLR_ENABLE_HIGH_ENTROPY    = 0x00000002

	// Dynamic code policy flags (ACG - Arbitrary Code Guard)
	PROCESS_DYNAMIC_CODE_DISALLOW = 0x00000001

	// Strict handle check
	PROCESS_STRICT_HANDLE_CHECKS = 0x00000001

	// Signature policy (CIG - Code Integrity Guard)
	PROCESS_SIGNATURE_POLICY_NONE           = 0x00000000
	PROCESS_SIGNATURE_POLICY_ALLOWED        = 0x00000001
	PROCESS_SIGNATURE_POLICY_STORE_ONLY     = 0x00000002
	PROCESS_SIGNATURE_POLICY_MITIGATED      = 0x00000004
	PROCESS_SIGNATURE_POLICY_MICROSOFT_ONLY = 0x00000008

	// Child process policy
	PROCESS_CHILD_PROCESS_RESTRICTED = 0x00000001

	// Font disable policy
	PROCESS_FONT_DISABLE = 0x00000001

	// Image load policy
	PROCESS_IMAGE_LOAD_REMOTE_MANDATORY    = 0x00000001
	PROCESS_IMAGE_LOAD_LOW_LABEL_MANDATORY = 0x00000004
	PROCESS_IMAGE_LOAD_PREFER_SYSTEM32     = 0x00000010

	// Job Object Information Class for UI restrictions
	jobObjectBasicUIRestrictionsClass = 4
)

// TOKEN_MANDATORY_LABEL represents the mandatory label structure
type TOKEN_MANDATORY_LABEL struct {
	Label windows.SIDAndAttributes
}

// Process mitigation policy structures
type ProcessMitigationDepPolicy struct {
	Flags uint32
	_     uint32
}

type ProcessMitigationAslrPolicy struct {
	Flags uint32
	_     uint32
}

type ProcessMitigationDynamicCodePolicy struct {
	Flags uint32
	_     uint32
}

type ProcessMitigationStrictHandleCheckPolicy struct {
	Flags uint32
	_     uint32
}

type ProcessMitigationSignaturePolicy struct {
	Flags uint32
	_     uint32
}

type ProcessMitigationChildProcessPolicy struct {
	Flags uint32
	_     uint32
}

type ProcessMitigationFontDisablePolicy struct {
	Flags uint32
	_     uint32
}

type ProcessMitigationImageLoadPolicy struct {
	Flags                    uint32
	PreferSystem32BinaryOnly uint32
}

// JobObjectUIRestrictions structure
type JobObjectUIRestrictions struct {
	UIRestrictionsClass uint32
}

// DangerousPrivileges is the list of privileges that should be stripped from sandboxed processes
var DangerousPrivileges = []string{
	"SeDebugPrivilege",
	"SeLoadDriverPrivilege",
	"SeBackupPrivilege",
	"SeRestorePrivilege",
	"SeShutdownPrivilege",
	"SeTakeOwnershipPrivilege",
	"SeSecurityPrivilege",
	"SeSystemEnvironmentPrivilege",
	"SeTcbPrivilege",
	"SeAssignPrimaryTokenPrivilege",
	"SeLockMemoryPrivilege",
	"SeIncreaseBasePriorityPrivilege",
	"SeCreatePagefilePrivilege",
	"SeCreatePermanentPrivilege",
	"SeSystemtimePrivilege",
	"SeProfileSingleProcessPrivilege",
	"SeManageVolumePrivilege",
	"SeImpersonatePrivilege",
	"SeCreateGlobalPrivilege",
	"SeAuditPrivilege",
	"SeIncreaseQuotaPrivilege",
	"SeRemoteShutdownPrivilege",
	"SeUndockPrivilege",
	"SeEnableDelegationPrivilege",
}

// DefaultEnvWhitelist - safe variables to pass to sandboxed processes
var DefaultEnvWhitelist = []string{
	"SystemRoot",
	"SystemDrive",
	"PATH",
	"PATHEXT",
	"TEMP",
	"TMP",
	"ComSpec",
	"OS",
	"PROCESSOR_ARCHITECTURE",
	"PROCESSOR_IDENTIFIER",
	"PROCESSOR_LEVEL",
	"PROCESSOR_REVISION",
	"NUMBER_OF_PROCESSORS",
	"USERPROFILE",
	"ALLUSERSPROFILE",
	"PUBLIC",
	"APPDATA",
	"LOCALAPPDATA",
	"PROGRAMDATA",
	"PROGRAMFILES",
	"PROGRAMFILES(X86)",
	"COMMONPROGRAMFILES",
	"COMMONPROGRAMFILES(X86)",
	"COMPUTERNAME",
	"LANG",
	"LC_ALL",
	"LC_CTYPE",
	"PYTHONIOENCODING",
	"NODE_OPTIONS",
}

var (
	win32InitOnce   sync.Once
	lowIntegritySid *windows.SID
)

func init() {
	win32InitOnce.Do(func() {
		// Create the Low Mandatory Level SID: S-1-16-4096
		authority := windows.SidIdentifierAuthority{
			Value: [6]byte{0, 0, 0, 0, 0, SECURITY_MANDATORY_LABEL_AUTHORITY_RID},
		}
		var sid *windows.SID
		err := windows.AllocateAndInitializeSid(
			&authority,
			1,
			uint32(SECURITY_MANDATORY_LOW_RID),
			0, 0, 0, 0, 0, 0, 0,
			&sid,
		)
		if err != nil {
			klog.Warningf("Failed to create Low Mandatory Level SID: %v", err)
		} else {
			lowIntegritySid = sid
		}
	})
}

// CreateSandboxToken creates a restricted, low-integrity token suitable for sandboxed processes
func CreateSandboxToken() (windows.Token, error) {
	var currentToken windows.Token
	err := windows.OpenProcessToken(
		windows.CurrentProcess(),
		windows.TOKEN_DUPLICATE|windows.TOKEN_ASSIGN_PRIMARY|windows.TOKEN_QUERY|windows.TOKEN_ADJUST_DEFAULT|windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_READ,
		&currentToken,
	)
	if err != nil {
		return 0, err
	}
	defer currentToken.Close()

	// Duplicate the token to get a primary token we can modify
	var duplicatedToken windows.Token
	err = windows.DuplicateTokenEx(
		currentToken,
		windows.MAXIMUM_ALLOWED,
		nil,
		windows.SecurityIdentification,
		windows.TokenPrimary,
		&duplicatedToken,
	)
	if err != nil {
		return 0, err
	}

	// Remove all dangerous privileges - they cannot be re-enabled once removed
	for _, privName := range DangerousPrivileges {
		privNamePtr, err := windows.UTF16PtrFromString(privName)
		if err != nil {
			continue
		}
		var luid windows.LUID
		err = windows.LookupPrivilegeValue(nil, privNamePtr, &luid)
		if err != nil {
			continue
		}

		tp := windows.Tokenprivileges{
			PrivilegeCount: 1,
		}
		tp.Privileges[0].Luid = luid
		tp.Privileges[0].Attributes = SE_PRIVILEGE_REMOVED

		windows.AdjustTokenPrivileges(duplicatedToken, false, &tp, 0, nil, nil)
	}

	// Set integrity level to Low
	if lowIntegritySid != nil {
		tml := TOKEN_MANDATORY_LABEL{}
		tml.Label.Sid = lowIntegritySid
		tml.Label.Attributes = windows.SE_GROUP_INTEGRITY | windows.SE_GROUP_INTEGRITY_ENABLED

		ret, _, _ := procSetTokenInformation.Call(
			uintptr(duplicatedToken),
			uintptr(TokenIntegrityLevel),
			uintptr(unsafe.Pointer(&tml)),
			uintptr(unsafe.Sizeof(tml)),
		)
		if ret == 0 {
			klog.V(4).Infof("Note: SetTokenInformation(IntegrityLevel) not available on this Windows version")
		}
	}

	return duplicatedToken, nil
}

// ApplyProcessMitigations sets all available process mitigation policies
func ApplyProcessMitigations(process windows.Handle) error {
	var appliedCount int
	var failedPolicies []string

	// Enable DEP (Data Execution Prevention) - always safe, prevents shellcode execution
	depPolicy := ProcessMitigationDepPolicy{
		Flags: PROCESS_DEP_ENABLE | PROCESS_DEP_DISABLE_ATL_THUNK_EMULATION,
	}
	if setMitigationPolicy(process, ProcessDEPPolicy, unsafe.Pointer(&depPolicy), uint32(unsafe.Sizeof(depPolicy))) {
		appliedCount++
	} else {
		failedPolicies = append(failedPolicies, "DEP")
	}

	// Enable ASLR with high entropy - always safe, memory randomization
	aslrPolicy := ProcessMitigationAslrPolicy{
		Flags: PROCESS_ASLR_ENABLE_RELOCATE_IMAGES | PROCESS_ASLR_ENABLE_HIGH_ENTROPY,
	}
	if setMitigationPolicy(process, ProcessASLRPolicy, unsafe.Pointer(&aslrPolicy), uint32(unsafe.Sizeof(aslrPolicy))) {
		appliedCount++
	} else {
		failedPolicies = append(failedPolicies, "ASLR")
	}

	// Note: ACG (Arbitrary Code Guard) is NOT enabled by default because it breaks
	// JIT compilers used by PowerShell/.NET/Python/Node.js and most modern languages.
	// It can be enabled as an optional strict policy for native-only workloads.

	// Enable strict handle checks - prevents invalid handle reuse, safe to enable
	strictHandlePolicy := ProcessMitigationStrictHandleCheckPolicy{
		Flags: PROCESS_STRICT_HANDLE_CHECKS,
	}
	if setMitigationPolicy(process, ProcessStrictHandleCheckPolicy, unsafe.Pointer(&strictHandlePolicy), uint32(unsafe.Sizeof(strictHandlePolicy))) {
		appliedCount++
	} else {
		failedPolicies = append(failedPolicies, "StrictHandle")
	}

	// Enable Code Integrity Guard (CIG) - allow Microsoft-signed binaries, not just Store
	// This blocks unsigned/injected DLLs while allowing standard Windows utilities
	signaturePolicy := ProcessMitigationSignaturePolicy{
		Flags: PROCESS_SIGNATURE_POLICY_MICROSOFT_ONLY | PROCESS_SIGNATURE_POLICY_ALLOWED,
	}
	if setMitigationPolicy(process, ProcessSignaturePolicy, unsafe.Pointer(&signaturePolicy), uint32(unsafe.Sizeof(signaturePolicy))) {
		appliedCount++
	} else {
		klog.V(5).Infof("CIG Microsoft-only signature policy not available, trying relaxed setting")
		// Fall back to no signature restriction if CIG not fully supported
	}

	// Note: We do NOT block child process creation! The Windows Job Object
	// automatically places all child processes in the same job, so they inherit
	// all resource limits and are terminated when the job closes. Blocking child
	// processes would prevent shells (cmd/powershell) from working at all.

	// Disable non-system fonts loading - helps prevent font parsing exploits
	fontPolicy := ProcessMitigationFontDisablePolicy{
		Flags: PROCESS_FONT_DISABLE,
	}
	if setMitigationPolicy(process, ProcessFontDisablePolicy, unsafe.Pointer(&fontPolicy), uint32(unsafe.Sizeof(fontPolicy))) {
		appliedCount++
	} else {
		failedPolicies = append(failedPolicies, "FontDisable")
	}

	// Block loading images from remote locations (network shares) - prevents loading exe/dll from SMB/WebDAV
	imageLoadPolicy := ProcessMitigationImageLoadPolicy{
		Flags:                    PROCESS_IMAGE_LOAD_REMOTE_MANDATORY | PROCESS_IMAGE_LOAD_LOW_LABEL_MANDATORY,
		PreferSystem32BinaryOnly: 0, // Don't force System32 preference, allows other binaries
	}
	if setMitigationPolicy(process, ProcessImageLoadPolicy, unsafe.Pointer(&imageLoadPolicy), uint32(unsafe.Sizeof(imageLoadPolicy))) {
		appliedCount++
	} else {
		failedPolicies = append(failedPolicies, "ImageLoad")
	}

	klog.V(2).Infof("Applied %d process mitigation policies", appliedCount)
	if len(failedPolicies) > 0 {
		klog.V(4).Infof("Some mitigations not available on this OS version: %v", failedPolicies)
	}

	return nil
}

func setMitigationPolicy(process windows.Handle, policyType int, policyInfo unsafe.Pointer, size uint32) bool {
	ret, _, _ := procSetProcessMitigationPolicy.Call(
		uintptr(process),
		uintptr(policyType),
		uintptr(policyInfo),
		uintptr(size),
	)
	return ret != 0
}

// ApplyJobUIRestrictions sets strict UI restrictions on the Job Object to prevent:
// - Clipboard access (read/write)
// - Global atom table access
// - Desktop/window station manipulation
// - Hooks installation
// - Global handle access
func ApplyJobUIRestrictions(job windows.Handle) error {
	uiRestrictions := JobObjectUIRestrictions{
		UIRestrictionsClass: JOB_OBJECT_UILIMIT_HANDLES |
			JOB_OBJECT_UILIMIT_READCLIPBOARD |
			JOB_OBJECT_UILIMIT_WRITECLIPBOARD |
			JOB_OBJECT_UILIMIT_SYSTEMPARAMETERS |
			JOB_OBJECT_UILIMIT_DISPLAYSETTINGS |
			JOB_OBJECT_UILIMIT_GLOBALATOMS |
			JOB_OBJECT_UILIMIT_DESKTOP |
			JOB_OBJECT_UILIMIT_EXITWINDOWS,
	}

	ret, _, err := kernel32.NewProc("SetInformationJobObject").Call(
		uintptr(job),
		uintptr(jobObjectBasicUIRestrictionsClass),
		uintptr(unsafe.Pointer(&uiRestrictions)),
		uintptr(unsafe.Sizeof(uiRestrictions)),
	)
	if ret == 0 {
		return fmt.Errorf("SetInformationJobObject(UIRestrictions) failed: %w", err)
	}
	klog.V(2).Infof("Job Object UI restrictions applied successfully")
	return nil
}

// BuildSanitizedEnvironment creates a clean environment block with only whitelisted variables
// plus explicitly allowed custom variables
func BuildSanitizedEnvironment(customEnv map[string]string) []string {
	whitelist := make(map[string]bool)
	for _, v := range DefaultEnvWhitelist {
		whitelist[strings.ToUpper(v)] = true
	}

	var env []string

	// Add whitelisted system variables
	for _, e := range os.Environ() {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToUpper(parts[0])
		if whitelist[key] {
			env = append(env, e)
		}
	}

	// Add/override with custom user variables
	for k, v := range customEnv {
		env = append(env, k+"="+v)
	}

	// Ensure PATH is restricted to system locations to prevent DLL sideloading
	pathSet := false
	for i, e := range env {
		if strings.HasPrefix(strings.ToUpper(e), "PATH=") {
			pathSet = true
			safePaths := []string{
				`C:\Windows\System32`,
				`C:\Windows`,
				`C:\Windows\System32\Wbem`,
				`C:\Windows\System32\WindowsPowerShell\v1.0\`,
			}
			env[i] = "PATH=" + strings.Join(safePaths, ";")
			break
		}
	}
	if !pathSet {
		safePaths := []string{
			`C:\Windows\System32`,
			`C:\Windows`,
			`C:\Windows\System32\Wbem`,
			`C:\Windows\System32\WindowsPowerShell\v1.0\`,
		}
		env = append(env, "PATH="+strings.Join(safePaths, ";"))
	}

	return env
}

// CreateProcessWithToken attempts to create a process with the given restricted token
func CreateProcessWithToken(token windows.Token, commandLine, workingDir string, env []string) (procHandle, threadHandle windows.Handle, pid uint32, err error) {
	cmdLine, err := windows.UTF16PtrFromString(commandLine)
	if err != nil {
		return 0, 0, 0, err
	}

	var workDirPtr *uint16
	if workingDir != "" {
		workDirPtr, err = windows.UTF16PtrFromString(workingDir)
		if err != nil {
			return 0, 0, 0, err
		}
	}

	// Convert env to block
	var envBlock *uint16
	if len(env) > 0 {
		envBlock, err = createEnvBlock(env)
		if err != nil {
			return 0, 0, 0, err
		}
		defer freeEnvBlock(envBlock)
	}

	var si windows.StartupInfo
	si.Cb = uint32(unsafe.Sizeof(si))
	var pi windows.ProcessInformation

	const LOGON_WITH_PROFILE = 0x00000001
	const CREATE_SUSPENDED = 0x00000004
	const CREATE_UNICODE_ENVIRONMENT = 0x00000400
	const CREATE_NEW_PROCESS_GROUP = 0x00000200
	const CREATE_DEFAULT_ERROR_MODE = 0x04000000

	creationFlags := uint32(CREATE_SUSPENDED | CREATE_NEW_PROCESS_GROUP | CREATE_UNICODE_ENVIRONMENT | CREATE_DEFAULT_ERROR_MODE)

	ret, _, createErr := procCreateProcessWithTokenW.Call(
		uintptr(token),
		uintptr(LOGON_WITH_PROFILE),
		0,
		uintptr(unsafe.Pointer(cmdLine)),
		0,
		uintptr(creationFlags),
		uintptr(unsafe.Pointer(envBlock)),
		uintptr(unsafe.Pointer(workDirPtr)),
		uintptr(unsafe.Pointer(&si)),
		uintptr(unsafe.Pointer(&pi)),
	)

	if ret == 0 {
		return 0, 0, 0, fmt.Errorf("CreateProcessWithTokenW failed: %w", createErr)
	}

	return pi.Process, pi.Thread, pi.ProcessId, nil
}

// createEnvBlock converts an []string environment to a Windows environment block
func createEnvBlock(env []string) (*uint16, error) {
	// Each entry is "KEY=VALUE\0", block ends with additional \0
	var size int
	for _, e := range env {
		size += len(e) + 1 // +1 for null terminator
	}
	size += 1 // extra null terminator for block end

	block := make([]uint16, size)
	offset := 0
	for _, e := range env {
		u16, err := windows.UTF16FromString(e)
		if err != nil {
			return nil, err
		}
		copy(block[offset:], u16)
		offset += len(u16)
		// already null terminated from UTF16FromString
	}
	return &block[0], nil
}

// freeEnvBlock frees the environment block created by createEnvBlock
func freeEnvBlock(block *uint16) {
	// Block is allocated from Go heap, will be GC'd, nothing to do
}

// AddFirewallBlockRule adds a Windows Firewall rule to block all network traffic for the given process path
func AddFirewallBlockRule(ruleName, processPath string) error {
	// Use netsh advfirewall to add the block rule
	cmd := exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		"name="+ruleName,
		"dir=out",
		"action=block",
		"program="+processPath,
		"enable=yes",
		"profile=any",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to add firewall rule: %w, output: %s", err, string(output))
	}
	klog.V(2).Infof("Added firewall block rule '%s' for process: %s", ruleName, processPath)
	return nil
}

// RemoveFirewallRule removes a previously added Windows Firewall rule by name
func RemoveFirewallRule(ruleName string) error {
	cmd := exec.Command("netsh", "advfirewall", "firewall", "delete", "rule",
		"name="+ruleName,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		klog.V(4).Infof("Failed to remove firewall rule '%s' (may not exist): %v, output: %s", ruleName, err, string(output))
		return nil // Don't fail cleanup
	}
	klog.V(2).Infof("Removed firewall rule '%s'", ruleName)
	return nil
}
