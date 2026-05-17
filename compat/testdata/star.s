; '*' is the address of the current instruction (ca65 semantics),
; not the byte after it — so `jmp *` is the classic spin-to-self.
loop:
        jmp *           ; jump to this instruction
        lda #<*         ; low byte of THIS instruction's address
        ldx #>*
        nop
here:   .word *         ; a data word holds its own address
        .byte <*, >*
